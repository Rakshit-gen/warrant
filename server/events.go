package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kgo"
)

// broker fans events out to console streams in this process. It drops
// events for a listener that falls behind rather than slowing payments.
type broker struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newBroker() *broker { return &broker{subs: map[chan []byte]struct{}{}} }

func (b *broker) publish(kind string, v any) {
	msg, _ := json.Marshal(map[string]any{"kind": kind, "at": time.Now().UnixMilli(), "data": v})
	b.mu.Lock()
	defer b.mu.Unlock()
	for c := range b.subs {
		select {
		case c <- msg:
		default:
		}
	}
}

func (b *broker) subscribe() (chan []byte, func()) {
	c := make(chan []byte, 64)
	b.mu.Lock()
	b.subs[c] = struct{}{}
	b.mu.Unlock()
	return c, func() {
		b.mu.Lock()
		delete(b.subs, c)
		b.mu.Unlock()
	}
}

// handleEvents streams decisions, step-ups, debits and revocations as
// server-sent events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	c, done := s.events.subscribe()
	defer done()
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-c:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// RelayOutbox sends outbox rows to Kafka until ctx ends. Rows are written
// in the same transaction as the change they describe, so a consumer sees
// every committed change at least once, keyed by mandate so one mandate's
// events stay in order on a partition.
func (s *Server) RelayOutbox(ctx context.Context, brokers []string) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.AllowAutoTopicCreation(), kgo.RequiredAcks(kgo.AllISRAcks()))
	if err != nil {
		return err
	}
	defer cl.Close()
	for ctx.Err() == nil {
		n, err := s.relayOnce(ctx, cl)
		if err != nil && ctx.Err() == nil {
			s.log.Error("outbox relay", "err", err)
		}
		if n == 0 || err != nil {
			select {
			case <-ctx.Done():
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	return nil
}

func (s *Server) relayOnce(ctx context.Context, cl *kgo.Client) (int, error) {
	sent := 0
	err := s.tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, topic, key, payload FROM outbox WHERE sent_at IS NULL ORDER BY id LIMIT 500 FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		var ids []int64
		var recs []*kgo.Record
		for rows.Next() {
			var id int64
			var topic, key string
			var payload []byte
			if err := rows.Scan(&id, &topic, &key, &payload); err != nil {
				return err
			}
			ids = append(ids, id)
			recs = append(recs, &kgo.Record{Topic: topic, Key: []byte(key), Value: payload})
		}
		if err := rows.Err(); err != nil || len(ids) == 0 {
			return err
		}
		if err := cl.ProduceSync(ctx, recs...).FirstErr(); err != nil {
			return err
		}
		sent = len(ids)
		_, err = tx.Exec(ctx, `UPDATE outbox SET sent_at = now() WHERE id = ANY($1)`, ids)
		return err
	})
	if sent > 0 {
		s.m.add("warrant_outbox_sent_total", float64(sent))
	}
	return sent, err
}
