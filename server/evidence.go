package server

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/Rakshit-gen/warrant/evidence"
)

var errUnsettled = errors.New("the debit has not settled yet; evidence is built from final records")

// Pack assembles the evidence for a debit.
func (s *Server) Pack(ctx context.Context, debitID string) (evidence.Pack, error) {
	var reqID, mandateID, user, agent, status string
	err := s.db.QueryRow(ctx, `SELECT d.request_id, d.mandate_id, d.status, m.user_id, m.agent_id FROM debits d JOIN mandates m ON m.id = d.mandate_id WHERE d.id = $1`, debitID).
		Scan(&reqID, &mandateID, &status, &user, &agent)
	if err != nil {
		return evidence.Pack{}, err
	}
	if !slices.Contains([]string{"succeeded", "failed"}, status) {
		return evidence.Pack{}, errUnsettled
	}
	if err := s.waitSequenced(ctx, []string{user, agent, mandateID, reqID}); err != nil {
		return evidence.Pack{}, err
	}
	env, cp, err := s.latestCheckpoint(ctx)
	if err != nil {
		return evidence.Pack{}, err
	}
	rows, err := s.db.Query(ctx, `SELECT idx, kind, body FROM entries WHERE idx < $5 AND (
			(kind = 'user' AND subject = $1) OR
			(kind IN ('agent', 'agent_suspended') AND subject = $2) OR
			(kind IN ('mandate', 'revocation') AND subject = $3) OR
			(kind IN ('request', 'decision', 'stepup', 'debit') AND subject = $4))
		ORDER BY idx`, user, agent, mandateID, reqID, cp.Size)
	if err != nil {
		return evidence.Pack{}, err
	}
	defer rows.Close()
	p := evidence.Pack{Version: 1, Debit: debitID, ServerKey: s.cfg.Key.PubString(), Checkpoint: env}
	for rows.Next() {
		var e evidence.Entry
		if err := rows.Scan(&e.Index, &e.Kind, &e.Body); err != nil {
			return p, err
		}
		if e.Proof, err = s.proof(e.Index, cp.Size); err != nil {
			return p, err
		}
		p.Entries = append(p.Entries, e)
	}
	return p, rows.Err()
}

func (s *Server) handleEvidence(w http.ResponseWriter, r *http.Request) {
	p, err := s.Pack(r.Context(), r.PathValue("id"))
	switch {
	case notFound(err):
		writeErr(w, http.StatusNotFound, "no such debit")
	case errors.Is(err, errUnsettled):
		writeErr(w, http.StatusConflict, err.Error())
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusOK, p)
	}
}

// handleDispute rules on a claim about a debit from its evidence alone.
func (s *Server) handleDispute(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Debit string `json:"debit"`
		Claim string `json:"claim"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if !slices.Contains([]string{evidence.Unauthorized, evidence.NotDelivered, evidence.WrongItem}, b.Claim) {
		writeErr(w, http.StatusBadRequest, "claim must be unauthorized, not_delivered or wrong_item")
		return
	}
	p, err := s.Pack(r.Context(), b.Debit)
	switch {
	case notFound(err):
		writeErr(w, http.StatusNotFound, "no such debit")
		return
	case errors.Is(err, errUnsettled):
		writeErr(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rep := evidence.Verify(p)
	ruling := evidence.Rule(rep, b.Claim)
	id := newID("dsp")
	if _, err := s.db.Exec(r.Context(), `INSERT INTO disputes (id, debit_id, claim, ruling) VALUES ($1, $2, $3, $4)`, id, b.Debit, b.Claim, ruling); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.m.inc("warrant_disputes_total{liable=\"" + ruling.Liable + "\"}")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "ruling": ruling, "checks": rep.Checks})
}
