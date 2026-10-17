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

// execute sends a debit to the bank and records what came back. Before the
// call the debit is marked unknown, so a crash mid-call leaves it for the
// reconciler rather than looking unsent.
func (s *Server) execute(d Debit) Debit {
	s.executing.Add(1)
	defer s.executing.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if d.block == "" {
		if err := s.db.QueryRow(ctx, `SELECT block_ref FROM mandates WHERE id = $1`, d.Mandate).Scan(&d.block); err != nil {
			return d
		}
	}
	if err := s.db.QueryRow(ctx, `UPDATE debits SET status = 'unknown', attempts = attempts + 1, updated_at = $2 WHERE id = $1 AND status IN ('pending', 'unknown') RETURNING attempts`, d.ID, s.now()).Scan(&d.Attempts); err != nil {
		return d // someone else finished it
	}
	d.Status = "unknown"
	t0 := time.Now()
	err := s.cfg.Rail.Debit(ctx, d.block, d.ID, d.Amount)
	s.m.observe("warrant_bank_seconds", time.Since(t0).Seconds())
	switch {
	case err == nil:
		s.finish(&d, "succeeded", "")
	case errors.Is(err, bank.ErrDeclined):
		s.finish(&d, "failed", err.Error())
	default:
		// No answer. The money may have moved; only the bank knows.
		s.m.inc("warrant_bank_unknown_total")
		s.db.Exec(ctx, `UPDATE debits SET last_error = $2 WHERE id = $1`, d.ID, err.Error())
		d.Error = err.Error()
	}
	return d
}

// finish moves a debit to its final state, once.
func (s *Server) finish(d *Debit, status, errMsg string) {
	ctx := context.Background()
	err := s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE debits SET status = $2, last_error = nullif($3, ''), updated_at = $4, settled_at = $4 WHERE id = $1 AND status IN ('pending', 'unknown')`, d.ID, status, errMsg, s.now())
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if err := record(ctx, tx, "debit", d.Request, map[string]any{"type": "debit", "id": d.ID, "request": d.Request, "mandate": d.Mandate, "amount": d.Amount, "status": status, "attempts": d.Attempts, "error": errMsg, "at": s.now().UnixMilli()}); err != nil {
			return err
		}
		return outbox(ctx, tx, "warrant.debits", d.Mandate, map[string]any{"debit": d.ID, "request": d.Request, "status": status, "amount": d.Amount})
	})
	if err != nil {
		s.log.Error("finish debit", "debit", d.ID, "err", err)
		return
	}
	d.Status, d.Error = status, errMsg
	s.m.inc(fmt.Sprintf(`warrant_debits_total{status=%q}`, status))
	s.events.publish("debit", map[string]any{"id": d.ID, "request": d.Request, "mandate": d.Mandate, "amount": d.Amount, "status": status})
}

// Reconcile settles debits the bank never answered, and sends debits that
// were approved but never sent (a crash between commit and call). It asks
// the bank before ever sending again, and sends again under the same
// transaction ID, so the bank's own idempotency is the last guard.
func (s *Server) Reconcile(ctx context.Context, olderThan time.Duration) (int, error) {
	rows, err := s.db.Query(ctx, `SELECT d.id, d.request_id, d.mandate_id, d.merchant, d.amount, d.status, d.attempts, d.created_at, m.block_ref
		FROM debits d JOIN mandates m ON m.id = d.mandate_id
		WHERE d.status IN ('pending', 'unknown') AND d.updated_at < $1 ORDER BY d.updated_at LIMIT 500`, s.now().Add(-olderThan))
	if err != nil {
		return 0, err
	}
	type open struct {
		Debit
		created time.Time
	}
	var todo []open
	for rows.Next() {
		var o open
		if err := rows.Scan(&o.ID, &o.Request, &o.Mandate, &o.Merchant, &o.Amount, &o.Status, &o.Attempts, &o.created, &o.block); err != nil {
			return 0, err
		}
		todo = append(todo, o)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	done := 0
	for _, o := range todo {
		d := o.Debit
		if d.Status == "pending" {
			s.execute(d)
			done++
			continue
		}
		st, err := s.cfg.Rail.Status(ctx, d.ID)
		if err != nil {
			continue // ask again next round
		}
		switch st {
		case bank.Success:
			s.finish(&d, "succeeded", "")
		case bank.Declined:
			s.finish(&d, "failed", "declined by the bank")
		default:
			// The bank never saw it. Reserve Pay allows three attempts in a day.
			if d.Attempts < wire.ReservePayMaxRetries && s.now().Sub(o.created) < 24*time.Hour {
				s.execute(d)
			} else {
				s.finish(&d, "failed", fmt.Sprintf("not delivered after %d attempts", d.Attempts))
			}
		}
		done++
	}
	if done > 0 {
		s.m.add("warrant_reconciled_total", float64(done))
	}
	return done, nil
}

// existing finds an earlier request with this idempotency key.
func (s *Server) existing(ctx context.Context, agent, idem, digest string) (Result, bool, error) {
	var id, old string
	err := s.db.QueryRow(ctx, `SELECT id, digest FROM requests WHERE agent_id = $1 AND idem_key = $2`, agent, idem).Scan(&id, &old)
	if notFound(err) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	if old != digest {
		return Result{}, false, errors.New("idempotency key already used for a different request")
	}
	res, err := s.result(ctx, id)
	res.Duplicate = true
	return res, err == nil, err
}

func (s *Server) result(ctx context.Context, id string) (Result, error) {
	var res Result
	var dec policy.Decision
	err := s.db.QueryRow(ctx, `SELECT id, decision FROM requests WHERE id = $1`, id).Scan(&res.Request, &dec)
	if err != nil {
		return res, err
	}
	res.Outcome, res.Reasons = dec.Outcome, dec.Reasons
	var st, stStatus string
	if err := s.db.QueryRow(ctx, `SELECT id, status FROM stepups WHERE request_id = $1`, id).Scan(&st, &stStatus); err == nil {
		res.StepUp = &st
		switch stStatus {
		case "declined", "expired":
			res.Outcome = policy.Deny
		}
	}
	var d Debit
	err = s.db.QueryRow(ctx, `SELECT id, request_id, mandate_id, merchant, amount, status, attempts, coalesce(last_error, '') FROM debits WHERE request_id = $1`, id).
		Scan(&d.ID, &d.Request, &d.Mandate, &d.Merchant, &d.Amount, &d.Status, &d.Attempts, &d.Error)
	if err == nil {
		res.Debit = &d
		res.Outcome = policy.Approve
	}
	return res, nil
}

func (s *Server) handleGetRequest(w http.ResponseWriter, r *http.Request) {
	res, err := s.result(r.Context(), r.PathValue("id"))
	if notFound(err) {
		writeErr(w, http.StatusNotFound, "no such request")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleMandateRequests is the activity on a mandate, newest first.
func (s *Server) handleMandateRequests(w http.ResponseWriter, r *http.Request) {
	s.list(w, r, `SELECT coalesce(jsonb_agg(x ORDER BY x->>'at' DESC), '[]') FROM (
		SELECT jsonb_build_object('id', q.id, 'at', q.created_at, 'outcome', q.outcome, 'reasons', q.decision->'reasons',
			'request', convert_from(decode(q.envelope->>'payload', 'base64'), 'UTF8')::jsonb,
			'stepup', st.status, 'debit', d.id, 'debit_status', d.status) AS x
		FROM requests q LEFT JOIN stepups st ON st.request_id = q.id LEFT JOIN debits d ON d.request_id = q.id
		WHERE q.mandate_id = $1 ORDER BY q.created_at DESC LIMIT 200) t`, r.PathValue("id"))
}

func newID(prefix string) string {
	b := make([]byte, 10)
	rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
