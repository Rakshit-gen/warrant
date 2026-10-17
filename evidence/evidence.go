// Package evidence checks a dispute pack and says who should bear a
// disputed agent payment.
//
// A pack is every log record behind one debit (the user's and agent's
// registrations, the signed mandate, the signed request, the decision, the
// user's step-up answer if there was one, the revocation if there was one,
// the debit) with an inclusion proof for each against one signed
// checkpoint. Verify needs nothing but the pack: it checks the signatures,
// the proofs, that the records agree with each other, and it runs the
// policy again on the recorded input to see that the decision was the one
// the policy gives.
package evidence

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/Rakshit-gen/warrant/ledger"
	"github.com/Rakshit-gen/warrant/policy"
	"github.com/Rakshit-gen/warrant/sign"
	"github.com/Rakshit-gen/warrant/wire"
)

// Pack is the evidence for one debit.
type Pack struct {
	Version    int           `json:"version"`
	Debit      string        `json:"debit"`
	ServerKey  string        `json:"server_key"`
	Checkpoint sign.Envelope `json:"checkpoint"`
	Entries    []Entry       `json:"entries"`
}

// Entry is one log record and its proof.
type Entry struct {
	Index int64    `json:"index"`
	Kind  string   `json:"kind"`
	Body  []byte   `json:"body"`  // the exact bytes in the log, base64 in JSON
	Proof []string `json:"proof"` // hex hashes, leaf to root
}

// Check is one thing Verify looked at.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Facts are what the records establish, once verified.
type Facts struct {
	User, Agent, Developer string
	Scope                  wire.Scope
	Request                wire.DebitRequest
	RequestTS              int64
	Decision               policy.Decision
	Recomputed             policy.Decision
	CoveredByIntent        bool
	Intent                 *wire.IntentReceipt
	StepUp                 *wire.StepUp // the user's signed answer, if asked
	RevokedBeforeDecision  bool
	CompromisedSince       int64 // unix ms, 0 if the agent's key was never reported
	DebitStatus            string
	DebitAmount            int64
}

// Report is the outcome of Verify.
type Report struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
	Facts  *Facts  `json:"-"`
}

type record struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Key    string          `json:"key"`
	Device string          `json:"device_key"`
	Dev    string          `json:"developer"`
	Signed *sign.Envelope  `json:"signed"`
	Intent *sign.Envelope  `json:"intent"`
	Digest string          `json:"digest"`
	Policy int             `json:"policy"`
	Opts   policy.Options  `json:"options"`
	Input  json.RawMessage `json:"input"`
	Dec    policy.Decision `json:"decision"`
	Req    string          `json:"request"`
	Amount int64           `json:"amount"`
	Status string          `json:"status"`
	Since  int64           `json:"compromised_since"`
	At     int64           `json:"at"`
}

// Verify checks a pack.
func Verify(p Pack) (rep Report) {
	check := func(name string, ok bool, f string, a ...any) bool {
		c := Check{Name: name, OK: ok}
		if !ok {
			c.Detail = fmt.Sprintf(f, a...)
		}
		rep.Checks = append(rep.Checks, c)
		return ok
	}
	defer func() {
		rep.OK = !slices.ContainsFunc(rep.Checks, func(c Check) bool { return !c.OK })
		if !rep.OK {
			rep.Facts = nil
		}
	}()

	// The checkpoint and every record's place under it.
	var cp struct {
		Size int64  `json:"size"`
		Root string `json:"root"`
	}
	if !check("checkpoint is signed by the server", p.Checkpoint.Open(p.ServerKey, &cp) == nil, "signature does not verify against %s", p.ServerKey) {
		return rep
	}
	rootB, err := hex.DecodeString(cp.Root)
	if !check("checkpoint root is well formed", err == nil && len(rootB) == 32, "bad root %q", cp.Root) {
		return rep
	}
	root := ledger.Hash(rootB)
	recs := map[string][]record{}
	idx := map[string]int64{}
	for _, e := range p.Entries {
		var proof []ledger.Hash
		for _, h := range e.Proof {
			b, err := hex.DecodeString(h)
			if err != nil || len(b) != 32 {
				break
			}
			proof = append(proof, ledger.Hash(b))
		}
		err := ledger.VerifyInclusion(ledger.LeafHash(e.Body), int(e.Index), int(cp.Size), proof, root)
		check(fmt.Sprintf("%s record is entry %d of the log", e.Kind, e.Index), err == nil && len(proof) == len(e.Proof), "inclusion proof fails")
		var r record
		if err := json.Unmarshal(e.Body, &r); err != nil || r.Type != e.Kind {
			check(e.Kind+" record parses", false, "body is not a %s record", e.Kind)
			continue
		}
		recs[e.Kind] = append(recs[e.Kind], r)
		idx[e.Kind] = e.Index
	}
	one := func(kind string) (record, bool) {
		rs := recs[kind]
		return firstOr(rs), check("pack has the "+kind+" record", len(rs) == 1, "found %d", len(rs))
	}
	user, ok1 := one("user")
	agent, ok2 := one("agent")
	mand, ok3 := one("mandate")
	reqR, ok4 := one("request")
	dec, ok5 := one("decision")
	deb, ok6 := one("debit")
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) {
		return rep
	}

	f := &Facts{User: user.ID, Agent: agent.ID, Developer: agent.Dev}
	// The mandate, signed by the user's registered device key.
	if mand.Signed == nil || !check("mandate is signed by the user's device key", mand.Signed.Open(user.Device, &f.Scope) == nil, "signature does not verify") {
		return rep
	}
	check("mandate is the user's and the agent's", f.Scope.User == user.ID && f.Scope.Agent == agent.ID, "scope names %s/%s", f.Scope.User, f.Scope.Agent)
	check("mandate is within the rail's limits", f.Scope.Validate() == nil, "%v", f.Scope.Validate())

	// The request, signed by the agent's registered key.
	if reqR.Signed == nil || !check("request is signed by the agent's key", reqR.Signed.Open(agent.Key, &f.Request) == nil, "signature does not verify") {
		return rep
	}
	f.RequestTS = f.Request.TS
	check("request is on this mandate", f.Request.Mandate == f.Scope.ID, "request is for %s", f.Request.Mandate)

	// The decision is about this request, and is what the policy gives.
	check("decision is about this request", dec.Digest == reqR.Signed.Digest() && dec.Req == reqR.ID, "digest %s, request %s", dec.Digest, reqR.Signed.Digest())
	var in policy.Input
	if !check("decision input parses", json.Unmarshal(dec.Input, &in) == nil, "bad input") {
		return rep
	}
	check("decision saw the signed mandate", reflect.DeepEqual(in.Scope, f.Scope), "input scope differs from the signed one")
	check("decision saw the signed request", reflect.DeepEqual(in.Req, f.Request), "input request differs from the signed one")
	if reqR.Intent != nil {
		var ir wire.IntentReceipt
		ok := reqR.Intent.Open(user.Device, &ir) == nil
		if check("intent receipt is signed by the user's device key", ok || in.IntentProblem != "", "signature does not verify, and the decision did not flag it") && ok {
			f.Intent = &ir
			check("decision saw the signed intent", in.Intent != nil && reflect.DeepEqual(*in.Intent, ir), "input intent differs")
		}
	}
	check("policy version is known", dec.Policy == policy.Version, "version %d", dec.Policy)
	f.Decision = dec.Dec
	f.Recomputed = policy.Evaluate(in, dec.Opts)
	check("recomputed decision matches the recorded one", sameDecision(f.Recomputed, f.Decision), "recorded %s, policy gives %s", f.Decision.Outcome, f.Recomputed.Outcome)
	f.CoveredByIntent = in.Intent != nil && in.Intent.Covers(in.Req, in.Now)

	// The user's answer, when asked.
	if st, ok := recs["stepup"]; ok {
		var ans wire.StepUp
		if check("step-up answer is signed by the user's device key", len(st) == 1 && st[0].Signed != nil && st[0].Signed.Open(user.Device, &ans) == nil, "signature does not verify") {
			check("step-up answer is about this request", ans.Request == reqR.Signed.Digest(), "answer is for %s", ans.Request)
			f.StepUp = &ans
		}
	}
	if rv, ok := recs["revocation"]; ok && len(rv) == 1 {
		var r wire.Revocation
		if check("revocation is signed by the user's device key", rv[0].Signed != nil && rv[0].Signed.Open(user.Device, &r) == nil, "signature does not verify") {
			f.RevokedBeforeDecision = idx["revocation"] < idx["decision"]
		}
	}
	if su, ok := recs["agent_suspended"]; ok && len(su) == 1 {
		f.CompromisedSince = su[0].Since
	}

	// The money.
	check("debit is for this request", deb.Req == reqR.ID, "debit is for %s", deb.Req)
	check("debit amount is the requested amount", deb.Amount == f.Request.Amount, "debited %d, requested %d", deb.Amount, f.Request.Amount)
	f.DebitStatus, f.DebitAmount = deb.Status, deb.Amount
	rep.Facts = f
	return rep
}

func firstOr(rs []record) record {
	if len(rs) == 0 {
		return record{}
	}
	return rs[0]
}

func sameDecision(a, b policy.Decision) bool {
	if a.Outcome != b.Outcome || len(a.Reasons) != len(b.Reasons) {
		return false
	}
	for i := range a.Reasons {
		if a.Reasons[i].Code != b.Reasons[i].Code || a.Reasons[i].Hard != b.Reasons[i].Hard {
			return false
		}
	}
	return true
}
