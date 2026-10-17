package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Rakshit-gen/warrant/bank"
	"github.com/Rakshit-gen/warrant/sign"
	"github.com/Rakshit-gen/warrant/wire"
)

// Registration. In production these come from onboarding (the user's UPI
// app, the agent developer's KYB); here an admin token stands in.

type userReq struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	DeviceKey string `json:"device_key"`
}

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	var u userReq
	if !readJSON(w, r, &u) {
		return
	}
	if _, err := sign.ParsePub(u.DeviceKey); err != nil || u.ID == "" {
		writeErr(w, http.StatusBadRequest, "id and a base64 Ed25519 device_key are required")
		return
	}
	err := s.tx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO users (id, name, device_key, created_at) VALUES ($1, $2, $3, $4)`, u.ID, u.Name, u.DeviceKey, s.now()); err != nil {
			return err
		}
		return record(r.Context(), tx, "user", u.ID, map[string]any{"type": "user", "id": u.ID, "device_key": u.DeviceKey, "at": s.now().UnixMilli()})
	})
	s.created(w, u, err)
}

type agentReq struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Developer string `json:"developer"`
	Key       string `json:"key"`
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	var a agentReq
	if !readJSON(w, r, &a) {
		return
	}
	if _, err := sign.ParsePub(a.Key); err != nil || a.ID == "" || a.Developer == "" {
		writeErr(w, http.StatusBadRequest, "id, developer and a base64 Ed25519 key are required")
		return
	}
	err := s.tx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO agents (id, name, developer, key, created_at) VALUES ($1, $2, $3, $4, $5)`, a.ID, a.Name, a.Developer, a.Key, s.now()); err != nil {
			return err
		}
		return record(r.Context(), tx, "agent", a.ID, map[string]any{"type": "agent", "id": a.ID, "developer": a.Developer, "key": a.Key, "at": s.now().UnixMilli()})
	})
	s.created(w, a, err)
}

// handleSuspend takes an agent off the rail. With compromised_since, the
// developer says its key leaked at that moment: requests signed after it
// are the developer's liability in a dispute.
func (s *Server) handleSuspend(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Reason           string `json:"reason"`
		CompromisedSince int64  `json:"compromised_since,omitempty"` // unix ms
	}
	if !readJSON(w, r, &b) {
		return
	}
	id := r.PathValue("id")
	var since *time.Time
	if b.CompromisedSince > 0 {
		t := time.UnixMilli(b.CompromisedSince)
		since = &t
	}
	err := s.tx(r.Context(), func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE agents SET status = 'suspended', compromised_since = $2 WHERE id = $1`, id, since)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return record(r.Context(), tx, "agent_suspended", id, map[string]any{"type": "agent_suspended", "id": id, "reason": b.Reason, "compromised_since": b.CompromisedSince, "at": s.now().UnixMilli()})
	})
	if notFound(err) {
		writeErr(w, http.StatusNotFound, "no such agent")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.events.publish("agent", map[string]any{"id": id, "status": "suspended"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "suspended"})
}

type merchantReq struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	VPA  string `json:"vpa"`
}

func (s *Server) handleMerchant(w http.ResponseWriter, r *http.Request) {
	var m merchantReq
	if !readJSON(w, r, &m) {
		return
	}
	if m.ID == "" || m.VPA == "" {
		writeErr(w, http.StatusBadRequest, "id and vpa are required")
		return
	}
	_, err := s.db.Exec(r.Context(), `INSERT INTO merchants (id, name, vpa, created_at) VALUES ($1, $2, $3, $4)`, m.ID, m.Name, m.VPA, s.now())
	s.created(w, m, err)
}

func (s *Server) handleMerchants(w http.ResponseWriter, r *http.Request) {
	s.list(w, r, `SELECT coalesce(jsonb_agg(jsonb_build_object('id', id, 'name', name, 'vpa', vpa) ORDER BY id), '[]') FROM merchants`)
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	s.list(w, r, `SELECT coalesce(jsonb_agg(jsonb_build_object('id', id, 'name', name, 'developer', developer, 'status', status) ORDER BY id), '[]') FROM agents`)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request, q string, args ...any) {
	var out json.RawMessage
	if err := s.db.QueryRow(r.Context(), q, args...).Scan(&out); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) created(w http.ResponseWriter, v any, err error) {
	switch {
	case isUnique(err):
		writeErr(w, http.StatusConflict, "already exists")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusCreated, v)
	}
}

// Mandate is a mandate as the API shows it.
type Mandate struct {
	ID        string        `json:"id"`
	User      string        `json:"user"`
	Agent     string        `json:"agent"`
	Kind      string        `json:"kind"`
	Scope     wire.Scope    `json:"scope"`
	Signed    sign.Envelope `json:"signed"`
	BlockRef  string        `json:"block_ref"`
	Status    string        `json:"status"`
	Spent     int64         `json:"spent"`     // settled this period
	InFlight  int64         `json:"in_flight"` // approved, not settled
	CreatedAt time.Time     `json:"created_at"`
	ExpiresAt time.Time     `json:"expires_at"`
	RevokedAt *time.Time    `json:"revoked_at,omitempty"`
}

// handleMandate takes a scope signed by the user's device key, asks the
// issuer to block the funds, and records it.
func (s *Server) handleMandate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var env sign.Envelope
	if !readJSON(w, r, &env) {
		return
	}
	var sc wire.Scope
	if err := json.Unmarshal(env.Payload, &sc); err != nil {
		writeErr(w, http.StatusBadRequest, "payload is not a scope")
		return
	}
	if err := sc.Validate(); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	var deviceKey, agentStatus string
	if err := s.db.QueryRow(ctx, `SELECT u.device_key, a.status FROM users u, agents a WHERE u.id = $1 AND a.id = $2`, sc.User, sc.Agent).Scan(&deviceKey, &agentStatus); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "unknown user or agent")
		return
	}
	if err := env.VerifyBy(deviceKey); err != nil {
		writeErr(w, http.StatusUnauthorized, "scope is not signed by the user's device key")
		return
	}
	if agentStatus != "active" {
		writeErr(w, http.StatusUnprocessableEntity, "agent is "+agentStatus)
		return
	}
	now := s.now()
	if now.Unix() >= sc.Expires {
		writeErr(w, http.StatusUnprocessableEntity, "scope has already expired")
		return
	}
	merchant := ""
	if sc.Kind == wire.ReservePay {
		merchant = sc.Merchants[0]
	} else {
		merchant = "circle:" + sc.Agent
	}
	block, err := s.cfg.Rail.Block(ctx, bank.BlockReq{User: sc.User, Merchant: merchant, Kind: sc.Kind, Amount: sc.Cap, Expires: time.Unix(sc.Expires, 0)})
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, bank.ErrDeclined) {
			code = http.StatusUnprocessableEntity
		}
		writeErr(w, code, "issuer: "+err.Error())
		return
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO mandates (id, user_id, agent_id, kind, scope, block_ref, created_at, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			sc.ID, sc.User, sc.Agent, sc.Kind, env, block, now, time.Unix(sc.Expires, 0)); err != nil {
			return err
		}
		if err := record(ctx, tx, "mandate", sc.ID, map[string]any{"type": "mandate", "id": sc.ID, "signed": env, "block_ref": block, "at": now.UnixMilli()}); err != nil {
			return err
		}
		return outbox(ctx, tx, "warrant.mandates", sc.ID, map[string]any{"event": "created", "mandate": sc.ID, "user": sc.User, "agent": sc.Agent})
	})
	if err != nil {
		// The block is orphaned at the issuer; release it.
		s.cfg.Rail.Revoke(context.WithoutCancel(ctx), block)
		if isUnique(err) {
			writeErr(w, http.StatusConflict, "mandate id already used")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.m.inc("warrant_mandates_created_total")
	s.events.publish("mandate", map[string]any{"id": sc.ID, "user": sc.User, "status": "active"})
	m, _ := s.mandate(ctx, s.db, sc.ID)
	writeJSON(w, http.StatusCreated, m)
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Server) mandate(ctx context.Context, q querier, id string) (Mandate, error) {
	var m Mandate
	err := q.QueryRow(ctx, `SELECT id, user_id, agent_id, kind, scope, block_ref, status, created_at, expires_at, revoked_at FROM mandates WHERE id = $1`, id).
		Scan(&m.ID, &m.User, &m.Agent, &m.Kind, &m.Signed, &m.BlockRef, &m.Status, &m.CreatedAt, &m.ExpiresAt, &m.RevokedAt)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(m.Signed.Payload, &m.Scope); err != nil {
		return m, err
	}
	start := periodStart(m.Kind, m.CreatedAt, s.now())
	err = q.QueryRow(ctx, `SELECT coalesce(sum(amount) FILTER (WHERE status = 'succeeded'), 0), coalesce(sum(amount) FILTER (WHERE status IN ('pending', 'unknown')), 0)
		FROM debits WHERE mandate_id = $1 AND created_at >= $2`, id, start).Scan(&m.Spent, &m.InFlight)
	return m, err
}

// periodStart is when the cap last reset: never for a Reserve Pay block,
// at the start of the calendar month for a Circle delegation.
func periodStart(kind string, created, now time.Time) time.Time {
	if kind == wire.Circle {
		y, mo, _ := now.Date()
		return time.Date(y, mo, 1, 0, 0, 0, 0, now.Location())
	}
	return created.Add(-time.Second)
}

func (s *Server) handleGetMandate(w http.ResponseWriter, r *http.Request) {
	m, err := s.mandate(r.Context(), s.db, r.PathValue("id"))
	if notFound(err) {
		writeErr(w, http.StatusNotFound, "no such mandate")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleUserMandates(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT id FROM mandates WHERE user_id = $1 ORDER BY created_at DESC LIMIT 100`, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []Mandate{}
	for _, id := range ids {
		m, err := s.mandate(r.Context(), s.db, id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRevoke ends a mandate on the user's signed word. Once this commits
// no new debit can be approved on it: approval takes the same row lock.
func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	var env sign.Envelope
	if !readJSON(w, r, &env) {
		return
	}
	var rv wire.Revocation
	var block string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var deviceKey, status string
		if err := tx.QueryRow(ctx, `SELECT u.device_key, m.status, m.block_ref FROM mandates m JOIN users u ON u.id = m.user_id WHERE m.id = $1 FOR UPDATE OF m`, id).Scan(&deviceKey, &status, &block); err != nil {
			return err
		}
		if err := env.Open(deviceKey, &rv); err != nil || rv.Type != "revoke" || rv.Mandate != id {
			return errUnauthorized
		}
		if status != "active" {
			return errAlready
		}
		if _, err := tx.Exec(ctx, `UPDATE mandates SET status = 'revoked', revoked_at = $2 WHERE id = $1`, id, s.now()); err != nil {
			return err
		}
		if err := record(ctx, tx, "revocation", id, map[string]any{"type": "revocation", "mandate": id, "signed": env, "at": s.now().UnixMilli()}); err != nil {
			return err
		}
		return outbox(ctx, tx, "warrant.mandates", id, map[string]any{"event": "revoked", "mandate": id})
	})
	switch {
	case notFound(err):
		writeErr(w, http.StatusNotFound, "no such mandate")
		return
	case errors.Is(err, errUnauthorized):
		writeErr(w, http.StatusUnauthorized, "revocation is not signed by the mandate's user")
		return
	case errors.Is(err, errAlready):
		writeErr(w, http.StatusConflict, "mandate is not active")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Release the funds. If the issuer is unreachable the block still
	// lapses at expiry, and warrant will not approve anything on it.
	if err := s.cfg.Rail.Revoke(context.WithoutCancel(ctx), block); err != nil {
		s.log.Warn("issuer revoke failed", "mandate", id, "err", err)
	}
	s.m.inc("warrant_mandates_revoked_total")
	s.events.publish("mandate", map[string]any{"id": id, "status": "revoked"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

