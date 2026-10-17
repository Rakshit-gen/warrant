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

