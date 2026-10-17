package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Rakshit-gen/warrant/bank"
	"github.com/Rakshit-gen/warrant/policy"
	"github.com/Rakshit-gen/warrant/sign"
	"github.com/Rakshit-gen/warrant/wire"
)

// ist is the day boundary for per-day limits: Indian users, Indian days.
var ist = time.FixedZone("IST", 5*3600+1800)

// DebitBody is what an agent posts.
type DebitBody struct {
	Request sign.Envelope  `json:"request"`
	Intent  *sign.Envelope `json:"intent,omitempty"`
}

// Debit is a payment on its way to, or back from, the bank.
type Debit struct {
	ID       string `json:"id"`
	Request  string `json:"request"`
	Mandate  string `json:"mandate"`
	Merchant string `json:"merchant"`
	Amount   int64  `json:"amount"`
	Status   string `json:"status"`
	Attempts int    `json:"attempts"`
	Error    string `json:"error,omitempty"`
	block    string
}

// Result is the answer to a debit request.
type Result struct {
	Request   string          `json:"request"`
	Outcome   string          `json:"outcome"`
	Reasons   []policy.Reason `json:"reasons"`
	StepUp    *string         `json:"stepup,omitempty"`
	Debit     *Debit          `json:"debit,omitempty"`
	Duplicate bool            `json:"duplicate,omitempty"`
}

func (r Result) code() int {
	switch r.Outcome {
	case policy.Approve:
		return http.StatusOK
	case policy.StepUp:
		return http.StatusAccepted
	}
	return http.StatusForbidden
}

func (s *Server) handleDebit(w http.ResponseWriter, r *http.Request) {
	var b DebitBody
	if !readJSON(w, r, &b) {
		return
	}
	res, code, err := s.Debit(r.Context(), b)
	if err != nil {
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, res.code(), res)
}

// Debit decides an agent's request and, if approved, pays it. The error
// cases are requests that never reach policy: unparseable, unsigned,
// replayed. Their HTTP status comes back with the error.
func (s *Server) Debit(ctx context.Context, b DebitBody) (Result, int, error) {
	start := time.Now()
	var req wire.DebitRequest
	if err := json.Unmarshal(b.Request.Payload, &req); err != nil || req.Type != "debit" || req.Mandate == "" || req.Agent == "" || req.Nonce == "" || req.IdemKey == "" {
		return Result{}, http.StatusBadRequest, errors.New("request payload is not a debit request")
	}
	var agentKey string
	if err := s.db.QueryRow(ctx, `SELECT key FROM agents WHERE id = $1`, req.Agent).Scan(&agentKey); err != nil {
		s.m.inc(`warrant_rejected_total{reason="agent_unknown"}`)
		return Result{}, http.StatusUnauthorized, fmt.Errorf("%s: agent %s is not registered", policy.AgentUnknown, req.Agent)
	}
	if err := b.Request.VerifyBy(agentKey); err != nil {
		s.m.inc(`warrant_rejected_total{reason="bad_signature"}`)
		return Result{}, http.StatusUnauthorized, errors.New("bad_signature: request is not signed by the agent's registered key")
	}
	digest := b.Request.Digest()

	// The same errand again: answer as before, never pay twice.
	if res, ok, err := s.existing(ctx, req.Agent, req.IdemKey, digest); err != nil {
		return Result{}, http.StatusConflict, err
	} else if ok {
		s.m.inc("warrant_duplicates_total")
		return res, 0, nil
	}

	var res Result
	var debit *Debit
	err := s.tx(ctx, func(tx pgx.Tx) error {
		in, intent, err := s.input(ctx, tx, req, b.Intent)
		if err != nil {
			return err
		}
		d := policy.Evaluate(in, s.cfg.Policy)
		id := newID("req")
		res = Result{Request: id, Outcome: d.Outcome, Reasons: d.Reasons}
		if _, err := tx.Exec(ctx, `INSERT INTO requests (id, agent_id, mandate_id, idem_key, nonce, digest, envelope, intent, input, decision, outcome, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			id, req.Agent, req.Mandate, req.IdemKey, req.Nonce, digest, b.Request, b.Intent, in, d, d.Outcome, s.now()); err != nil {
			return err
		}
		if err := record(ctx, tx, "request", id, map[string]any{"type": "request", "id": id, "signed": b.Request, "intent": intent, "at": s.now().UnixMilli()}); err != nil {
			return err
		}
		if err := record(ctx, tx, "decision", id, map[string]any{"type": "decision", "request": id, "digest": digest, "policy": policy.Version, "options": s.cfg.Policy, "input": in, "decision": d, "at": s.now().UnixMilli()}); err != nil {
			return err
		}
		switch d.Outcome {
		case policy.Approve:
			debit, err = s.openDebit(ctx, tx, id, req.Mandate)
			if err != nil {
				return err
			}
			res.Debit = debit
		case policy.StepUp:
			st := newID("stp")
			var user string
			if err := tx.QueryRow(ctx, `SELECT user_id FROM mandates WHERE id = $1`, req.Mandate).Scan(&user); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO stepups (id, request_id, user_id, created_at, expires_at) VALUES ($1,$2,$3,$4,$5)`,
				st, id, user, s.now(), s.now().Add(s.cfg.StepUpTTL)); err != nil {
				return err
			}
			res.StepUp = &st
		}
		return outbox(ctx, tx, "warrant.decisions", req.Mandate, map[string]any{"request": id, "mandate": req.Mandate, "agent": req.Agent, "outcome": d.Outcome, "reasons": d.Reasons, "amount": req.Amount})
	})
	switch {
	case errors.Is(err, errNoMandate):
		return Result{}, http.StatusNotFound, err
	case isUnique(err):
		// Same nonce under another idempotency key: a replay, or a client
		// reusing nonces. Either way, not a new payment.
		s.m.inc(`warrant_rejected_total{reason="replay"}`)
		return Result{}, http.StatusConflict, errors.New("replay: nonce already used")
	case err != nil:
		return Result{}, http.StatusInternalServerError, err
	}
	s.m.inc(fmt.Sprintf(`warrant_decisions_total{outcome=%q}`, res.Outcome))
	s.m.observe("warrant_decision_seconds", time.Since(start).Seconds())
	s.events.publish("decision", map[string]any{"request": res.Request, "mandate": req.Mandate, "agent": req.Agent, "merchant": req.Merchant, "amount": req.Amount, "outcome": res.Outcome, "reasons": res.Reasons})
	if debit != nil {
		final := s.execute(*debit)
		res.Debit = &final
	}
	return res, 0, nil
}

var errNoMandate = errors.New("no such mandate")

// input gathers what the policy needs, holding the mandate's row lock so
// that every limit is checked against settled and in-flight debits and no
// two approvals can race past the cap.
func (s *Server) input(ctx context.Context, tx pgx.Tx, req wire.DebitRequest, intentEnv *sign.Envelope) (policy.Input, *sign.Envelope, error) {
	now := s.now()
	in := policy.Input{Now: now, Req: req, Recent: []policy.Past{}, History: []int64{}}
	var userKey string
	var created time.Time
	var signed sign.Envelope
	err := tx.QueryRow(ctx, `SELECT m.scope, m.status, m.created_at, u.device_key FROM mandates m JOIN users u ON u.id = m.user_id WHERE m.id = $1 FOR UPDATE OF m`, req.Mandate).
		Scan(&signed, &in.MandateStatus, &created, &userKey)
	if notFound(err) {
		return in, nil, errNoMandate
	}
	if err != nil {
		return in, nil, err
	}
	if err := json.Unmarshal(signed.Payload, &in.Scope); err != nil {
		return in, nil, err
	}
	in.LinkedAt = created

	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM agents WHERE id = $1`, req.Agent).Scan(&status)
	in.AgentKnown, in.AgentStatus = err == nil, status
	tx.QueryRow(ctx, `SELECT vpa FROM merchants WHERE id = $1`, req.Merchant).Scan(&in.MerchantVPA)

	y, mo, d := now.In(ist).Date()
	dayStart := time.Date(y, mo, d, 0, 0, 0, 0, ist)
	err = tx.QueryRow(ctx, `SELECT coalesce(sum(amount) FILTER (WHERE created_at >= $2), 0),
			coalesce(sum(amount) FILTER (WHERE created_at >= $3), 0), count(*) FILTER (WHERE created_at >= $3)
		FROM debits WHERE mandate_id = $1 AND status <> 'failed'`, req.Mandate, periodStart(in.Scope.Kind, created, now), dayStart).
		Scan(&in.Spent, &in.SpentToday, &in.DebitsToday)
	if err != nil {
		return in, nil, err
	}

	rows, err := tx.Query(ctx, `SELECT created_at, merchant, amount, status FROM debits WHERE mandate_id = $1 AND status <> 'failed' AND created_at >= $2 ORDER BY created_at`, req.Mandate, now.Add(-90*24*time.Hour))
	if err != nil {
		return in, nil, err
	}
	for rows.Next() {
		var p policy.Past
		var st string
		if err := rows.Scan(&p.At, &p.Merchant, &p.Amount, &st); err != nil {
			return in, nil, err
		}
		p.At = p.At.UTC()
		if now.Sub(p.At) <= 24*time.Hour {
			in.Recent = append(in.Recent, p)
		}
		if st == "succeeded" {
			in.History = append(in.History, p.Amount)
		}
	}
	if err := rows.Err(); err != nil {
		return in, nil, err
	}
	if n := len(in.History); n > 50 {
		in.History = in.History[n-50:]
	}

	if intentEnv != nil {
		var ir wire.IntentReceipt
		switch err := intentEnv.Open(userKey, &ir); {
		case err != nil:
			in.IntentProblem = "intent receipt is not signed by the user's device key"
		case ir.Type != "intent" || ir.User != in.Scope.User:
			in.IntentProblem = "intent receipt is for another user"
		case ir.ID != req.IntentID:
			in.IntentProblem = "request names a different intent receipt"
		default:
			in.Intent = &ir
		}
	} else if req.IntentID != "" {
		in.IntentProblem = "request names an intent receipt but none was sent"
	}
	in.Now = now.UTC()
	in.LinkedAt = in.LinkedAt.UTC()
	return in, intentEnv, nil
}

var errLimit = errors.New("limit")

// openDebit puts a debit against the mandate's limits, inside tx. The
// caller has decided to pay; this is the last check that the money is
// still there and the mandate still stands.
func (s *Server) openDebit(ctx context.Context, tx pgx.Tx, reqID, mandateID string) (*Debit, error) {
	var signed sign.Envelope
	var status, block string
	var created time.Time
	if err := tx.QueryRow(ctx, `SELECT scope, status, block_ref, created_at FROM mandates WHERE id = $1 FOR UPDATE`, mandateID).Scan(&signed, &status, &block, &created); err != nil {
		return nil, err
	}
	var sc wire.Scope
	json.Unmarshal(signed.Payload, &sc)
	var env sign.Envelope
	if err := tx.QueryRow(ctx, `SELECT envelope FROM requests WHERE id = $1`, reqID).Scan(&env); err != nil {
		return nil, err
	}
	var req wire.DebitRequest
	json.Unmarshal(env.Payload, &req)
	if status != "active" || s.now().Unix() >= sc.Expires {
		return nil, fmt.Errorf("%w: mandate is %s", errLimit, status)
	}
	var used int64
	if err := tx.QueryRow(ctx, `SELECT coalesce(sum(amount), 0) FROM debits WHERE mandate_id = $1 AND status <> 'failed' AND created_at >= $2`,
		mandateID, periodStart(sc.Kind, created, s.now())).Scan(&used); err != nil {
		return nil, err
	}
	if used+req.Amount > sc.Cap {
		return nil, fmt.Errorf("%w: only %s of the cap is left", errLimit, wire.Rupees(max(0, sc.Cap-used)))
	}
	d := &Debit{ID: newID("dbt"), Request: reqID, Mandate: mandateID, Merchant: req.Merchant, Amount: req.Amount, Status: "pending", block: block}
	_, err := tx.Exec(ctx, `INSERT INTO debits (id, request_id, mandate_id, merchant, amount, status, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,'pending',$6,$6)`,
		d.ID, reqID, mandateID, d.Merchant, d.Amount, s.now())
	return d, err
}

