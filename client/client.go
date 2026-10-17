// Package client is the Go SDK for warrant: an Agent signs and sends
// debit requests, a Device stands in for the user's UPI app (it holds the
// device key that signs mandates, intents, step-up answers and
// revocations), and Admin registers the parties.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Rakshit-gen/warrant/evidence"
	"github.com/Rakshit-gen/warrant/sign"
	"github.com/Rakshit-gen/warrant/wire"
)

// API is a connection to warrantd.
type API struct {
	Base  string
	Token string // admin token, for registration
	HTTP  *http.Client
	// Now stamps requests; nil means time.Now.
	Now func() time.Time
}

// New talks to warrantd at base.
func New(base string) *API {
	return &API{Base: base, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Error is a non-2xx answer.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return fmt.Sprintf("warrant: %d %s", e.Status, e.Msg) }

// Do sends in as JSON and decodes the answer into out. 2xx answers decode;
// 403 decodes too, since a denied debit is an answer, not a failure.
func (a *API) Do(ctx context.Context, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.Base+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusForbidden {
		var e struct{ Error string }
		json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = string(b)
		}
		return resp.StatusCode, &Error{resp.StatusCode, e.Error}
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

// Nonce is a fresh random nonce.
func Nonce() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Registration.

func (a *API) AddUser(ctx context.Context, id, name string, device sign.Key) error {
	_, err := a.Do(ctx, "POST", "/v1/users", map[string]string{"id": id, "name": name, "device_key": device.PubString()}, nil)
	return err
}

func (a *API) AddAgent(ctx context.Context, id, name, developer string, key sign.Key) error {
	_, err := a.Do(ctx, "POST", "/v1/agents", map[string]string{"id": id, "name": name, "developer": developer, "key": key.PubString()}, nil)
	return err
}

func (a *API) AddMerchant(ctx context.Context, id, name, vpa string) error {
	_, err := a.Do(ctx, "POST", "/v1/merchants", map[string]string{"id": id, "name": name, "vpa": vpa}, nil)
	return err
}

// SuspendAgent takes an agent off. compromisedSince, if not zero, is when
// its developer says the key leaked.
func (a *API) SuspendAgent(ctx context.Context, id, reason string, compromisedSince time.Time) error {
	body := map[string]any{"reason": reason}
	if !compromisedSince.IsZero() {
		body["compromised_since"] = compromisedSince.UnixMilli()
	}
	_, err := a.Do(ctx, "POST", "/v1/agents/"+id+"/suspend", body, nil)
	return err
}

// Device is the user's side: their UPI app and the key it holds.
type Device struct {
	API  *API
	User string
	Key  sign.Key
}

// Grant signs a scope and creates the mandate.
func (d Device) Grant(ctx context.Context, sc wire.Scope) (Mandate, error) {
	sc.Type, sc.User = "scope", d.User
	env, err := d.Key.Seal(sc)
	if err != nil {
		return Mandate{}, err
	}
	var m Mandate
	_, err = d.API.Do(ctx, "POST", "/v1/mandates", env, &m)
	return m, err
}

// Intent signs an intent receipt for one errand. The agent sends it along
// with its request.
func (d Device) Intent(ir wire.IntentReceipt) (sign.Envelope, error) {
	ir.Type, ir.User = "intent", d.User
	return d.Key.Seal(ir)
}

// Revoke ends a mandate.
func (d Device) Revoke(ctx context.Context, mandate string) error {
	env, err := d.Key.Seal(wire.Revocation{Type: "revoke", Mandate: mandate, TS: d.API.now().UnixMilli()})
	if err != nil {
		return err
	}
	_, err = d.API.Do(ctx, "POST", "/v1/mandates/"+mandate+"/revoke", env, nil)
	return err
}

// StepUp is a question waiting for the user.
type StepUp struct {
	ID       string          `json:"id"`
	Request  string          `json:"request"`
	Digest   string          `json:"digest"`
	Mandate  string          `json:"mandate"`
	Agent    string          `json:"agent"`
	Merchant string          `json:"merchant"`
	Amount   int64           `json:"amount"`
	Cart     []wire.Item     `json:"cart"`
	Reasons  json.RawMessage `json:"reasons"`
}

// Pending lists the user's open step-ups.
func (d Device) Pending(ctx context.Context) ([]StepUp, error) {
	var out []StepUp
	_, err := d.API.Do(ctx, "GET", "/v1/users/"+d.User+"/stepups", nil, &out)
	return out, err
}

// Answer approves or declines a step-up.
func (d Device) Answer(ctx context.Context, st StepUp, approve bool) (map[string]any, error) {
	env, err := d.Key.Seal(wire.StepUp{Type: "stepup", ID: st.ID, Request: st.Digest, Approve: approve, TS: d.API.now().UnixMilli()})
	if err != nil {
		return nil, err
	}
	var out map[string]any
	_, err = d.API.Do(ctx, "POST", "/v1/stepups/"+st.ID, env, &out)
	return out, err
}

// Mandate as the API returns it.
type Mandate struct {
	ID       string     `json:"id"`
	User     string     `json:"user"`
	Agent    string     `json:"agent"`
	Kind     string     `json:"kind"`
	Scope    wire.Scope `json:"scope"`
	BlockRef string     `json:"block_ref"`
	Status   string     `json:"status"`
	Spent    int64      `json:"spent"`
	InFlight int64      `json:"in_flight"`
}

// Agent is an AI agent's side: its ID and registered key.
type Agent struct {
	API *API
	ID  string
	Key sign.Key
}

// Result is warrant's answer to a debit request.
type Result struct {
	Request   string   `json:"request"`
	Outcome   string   `json:"outcome"`
	Reasons   []Reason `json:"reasons"`
	StepUp    *string  `json:"stepup"`
	Debit     *Debit   `json:"debit"`
	Duplicate bool     `json:"duplicate"`
}

// Debit is a payment warrant approved. Status is pending, unknown (sent,
// no answer from the bank yet), succeeded or failed.
type Debit struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Amount   int64  `json:"amount"`
	Attempts int    `json:"attempts"`
	Error    string `json:"error"`
}

// Reason is one check that fired.
type Reason struct {
	Code   string `json:"code"`
	Hard   bool   `json:"hard"` // a hard reason denies; a soft one asks the user
	Detail string `json:"detail"`
}

// Has reports whether the answer carries a reason code.
func (r Result) Has(code string) bool {
	for _, x := range r.Reasons {
		if x.Code == code {
			return true
		}
	}
	return false
}

// Pay signs and sends a debit request. Fields left empty are filled: the
// agent, a fresh nonce, the time, and an idempotency key if none is given.
// Retrying an errand should reuse its IdemKey.
func (a Agent) Pay(ctx context.Context, req wire.DebitRequest, intent *sign.Envelope) (Result, error) {
	req.Type, req.Agent = "debit", a.ID
	if req.Nonce == "" {
		req.Nonce = Nonce()
	}
	if req.TS == 0 {
		req.TS = a.API.now().UnixMilli()
	}
	if req.IdemKey == "" {
		req.IdemKey = Nonce()
	}
	env, err := a.Key.Seal(req)
	if err != nil {
		return Result{}, err
	}
	return a.Send(ctx, env, intent)
}

// Send posts an already signed request, as a retry or a replay would.
func (a Agent) Send(ctx context.Context, env sign.Envelope, intent *sign.Envelope) (Result, error) {
	var res Result
	_, err := a.API.Do(ctx, "POST", "/v1/debits", map[string]any{"request": env, "intent": intent}, &res)
	return res, err
}

// Poll fetches the current state of a request.
func (a *API) Poll(ctx context.Context, request string) (Result, error) {
	var res Result
	_, err := a.Do(ctx, "GET", "/v1/requests/"+request, nil, &res)
	return res, err
}

// Evidence fetches a debit's dispute pack.
func (a *API) Evidence(ctx context.Context, debit string) (evidence.Pack, error) {
	var p evidence.Pack
	_, err := a.Do(ctx, "GET", "/v1/debits/"+debit+"/evidence", nil, &p)
	return p, err
}

// Dispute asks for a ruling.
func (a *API) Dispute(ctx context.Context, debit, claim string) (evidence.Ruling, error) {
	var out struct {
		Ruling evidence.Ruling `json:"ruling"`
	}
	_, err := a.Do(ctx, "POST", "/v1/disputes", map[string]string{"debit": debit, "claim": claim}, &out)
	return out.Ruling, err
}
