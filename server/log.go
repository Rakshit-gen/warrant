package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Rakshit-gen/warrant/ledger"
	"github.com/Rakshit-gen/warrant/sign"
)

// Checkpoint is a signed tree head.
type Checkpoint struct {
	Type string `json:"type"` // "checkpoint"
	Size int64  `json:"size"`
	Root string `json:"root"` // hex
	TS   int64  `json:"ts"`   // unix milliseconds
}

// tree keeps every sequenced leaf hash in memory. A million records is 32
// MB and a root over them takes a few hundred milliseconds; past that the
// sequencer should keep a compact range instead of recomputing.
type tree struct {
	mu     sync.RWMutex
	leaves []ledger.Hash
}

func loadTree(ctx context.Context, db *pgxpool.Pool) (*tree, error) {
	rows, err := db.Query(ctx, `SELECT idx, body FROM entries WHERE idx IS NOT NULL ORDER BY idx`)
	if err != nil {
		return nil, err
	}
	t := &tree{}
	for rows.Next() {
		var idx int64
		var body []byte
		if err := rows.Scan(&idx, &body); err != nil {
			return nil, err
		}
		if idx != int64(len(t.leaves)) {
			return nil, fmt.Errorf("log: gap at leaf %d", len(t.leaves))
		}
		t.leaves = append(t.leaves, ledger.LeafHash(body))
	}
	return t, rows.Err()
}

func (t *tree) size() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return int64(len(t.leaves))
}

// record adds a log entry inside tx. The body is the exact JSON that will
// be hashed and handed out in evidence.
func record(ctx context.Context, tx pgx.Tx, kind, subject string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO entries (kind, subject, body) VALUES ($1, $2, $3)`, kind, subject, body)
	return err
}

// Sequence gives unsequenced entries their leaf indexes, in the order they
// committed, and signs a checkpoint over the new tree. It returns how many
// entries it placed. One server at a time sequences, by advisory lock.
func (s *Server) Sequence(ctx context.Context) (int, error) {
	var placed []ledger.Hash
	var cp sign.Envelope
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7243)`); err != nil {
			return err
		}
		var next int64
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(idx) + 1, 0) FROM entries`).Scan(&next); err != nil {
			return err
		}
		if next != s.tree.size() {
			// Another process sequenced; catch up from the database.
			t, err := loadTree(ctx, s.db)
			if err != nil {
				return err
			}
			s.tree = t
		}
		rows, err := tx.Query(ctx, `SELECT id, body FROM entries WHERE idx IS NULL ORDER BY id LIMIT 5000`)
		if err != nil {
			return err
		}
		var ids, idxs []int64
		for rows.Next() {
			var id int64
			var body []byte
			if err := rows.Scan(&id, &body); err != nil {
				return err
			}
			ids = append(ids, id)
			idxs = append(idxs, next+int64(len(ids)-1))
			placed = append(placed, ledger.LeafHash(body))
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE entries e SET idx = v.idx FROM unnest($1::bigint[], $2::bigint[]) AS v(id, idx) WHERE e.id = v.id`, ids, idxs); err != nil {
			return err
		}
		s.tree.mu.RLock()
		all := append(append([]ledger.Hash(nil), s.tree.leaves...), placed...)
		s.tree.mu.RUnlock()
		root := ledger.Root(all)
		cp, err = s.cfg.Key.Seal(Checkpoint{Type: "checkpoint", Size: int64(len(all)), Root: hex.EncodeToString(root[:]), TS: s.now().UnixMilli()})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO checkpoints (size, envelope) VALUES ($1, $2)`, len(all), cp)
		return err
	})
	if err != nil || len(placed) == 0 {
		return 0, err
	}
	s.tree.mu.Lock()
	s.tree.leaves = append(s.tree.leaves, placed...)
	s.tree.mu.Unlock()
	s.m.add("warrant_log_entries_total", float64(len(placed)))
	return len(placed), nil
}

// latestCheckpoint is the newest signed tree head.
func (s *Server) latestCheckpoint(ctx context.Context) (sign.Envelope, Checkpoint, error) {
	var env sign.Envelope
	err := s.db.QueryRow(ctx, `SELECT envelope FROM checkpoints ORDER BY size DESC LIMIT 1`).Scan(&env)
	if err != nil {
		return env, Checkpoint{}, err
	}
	var cp Checkpoint
	err = json.Unmarshal(env.Payload, &cp)
	return env, cp, err
}

// proof is the inclusion proof of leaf idx in the tree of the given size.
func (s *Server) proof(idx, size int64) ([]string, error) {
	s.tree.mu.RLock()
	defer s.tree.mu.RUnlock()
	if size > int64(len(s.tree.leaves)) || idx >= size {
		return nil, fmt.Errorf("log: leaf %d is not in a tree of %d", idx, size)
	}
	var out []string
	for _, h := range ledger.Proof(s.tree.leaves[:size], int(idx)) {
		out = append(out, hex.EncodeToString(h[:]))
	}
	return out, nil
}

func (s *Server) handleCheckpoint(w http.ResponseWriter, r *http.Request) {
	env, _, err := s.latestCheckpoint(r.Context())
	if notFound(err) {
		writeErr(w, http.StatusNotFound, "the log is empty")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checkpoint": env, "server_key": s.cfg.Key.PubString()})
}

// waitSequenced sequences until every entry about subject has a place,
// giving up after a few seconds.
func (s *Server) waitSequenced(ctx context.Context, subjects []string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		var pending int
		if err := s.db.QueryRow(ctx, `SELECT count(*) FROM entries WHERE idx IS NULL AND subject = ANY($1)`, subjects).Scan(&pending); err != nil {
			return err
		}
		if pending == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("log: %d entries still unsequenced", pending)
		}
		if _, err := s.Sequence(ctx); err != nil {
			return err
		}
	}
}
