package evidence

import (
	"testing"

	"github.com/Rakshit-gen/warrant/policy"
	"github.com/Rakshit-gen/warrant/wire"
)

func TestRuleOrder(t *testing.T) {
	approve := policy.Decision{Outcome: policy.Approve, Reasons: []policy.Reason{}}
	stepup := policy.Decision{Outcome: policy.StepUp, Reasons: []policy.Reason{{Code: policy.OverStepUp}}}
	deny := policy.Decision{Outcome: policy.Deny, Reasons: []policy.Reason{{Code: policy.MerchantOff, Hard: true}}}
	base := func(mod func(*Facts)) Report {
		f := &Facts{Agent: "bot", Developer: "acme", Decision: approve, Recomputed: approve, DebitStatus: "succeeded",
			RequestTS: 2000, Request: wire.DebitRequest{Merchant: "zepto"}, Scope: wire.Scope{Kind: wire.ReservePay}}
		if mod != nil {
			mod(f)
		}
		return Report{OK: true, Facts: f}
	}
	cases := []struct {
		name  string
		rep   Report
		claim string
		want  string
	}{
		{"unverifiable pack", Report{OK: false, Checks: []Check{{Name: "x", OK: false}}}, Unauthorized, Platform},
		{"nothing moved", base(func(f *Facts) { f.DebitStatus = "failed" }), Unauthorized, Nobody},
		{"recorded decision is not the policy's", base(func(f *Facts) { f.Recomputed = deny }), Unauthorized, Platform},
		{"paid on a deny", base(func(f *Facts) { f.Decision, f.Recomputed = deny, deny }), Unauthorized, Platform},
		{"paid a step-up without approval", base(func(f *Facts) { f.Decision, f.Recomputed = stepup, stepup }), Unauthorized, Platform},
		{"paid after revoke", base(func(f *Facts) { f.RevokedBeforeDecision = true }), Unauthorized, Platform},
		{"leaked key, signed after", base(func(f *Facts) { f.CompromisedSince = 1000 }), Unauthorized, Developer},
		{"leaked key, signed before", base(func(f *Facts) { f.CompromisedSince = 3000 }), Unauthorized, User},
		{"goods never came", base(nil), NotDelivered, Merchant},
		{"wrong goods", base(nil), WrongItem, Merchant},
		{"inside the standing scope", base(nil), Unauthorized, User},
		{"approved step-up", base(func(f *Facts) {
			f.Decision, f.Recomputed = stepup, stepup
			f.StepUp = &wire.StepUp{Approve: true, TS: 2500}
		}), Unauthorized, User},
	}
	for _, c := range cases {
		r := Rule(c.rep, c.claim)
		if r.Liable != c.want || len(r.Why) == 0 {
			t.Errorf("%s: %s carries it (%v), want %s", c.name, r.Liable, r.Why, c.want)
		}
		if r.Refund != (c.want != User && c.want != Nobody) {
			t.Errorf("%s: refund %v", c.name, r.Refund)
		}
	}
}
