package evidence

import (
	"fmt"
	"time"

	"github.com/Rakshit-gen/warrant/policy"
	"github.com/Rakshit-gen/warrant/wire"
)

// Claims a user can make about an agent payment.
const (
	Unauthorized = "unauthorized"  // "I did not want this"
	NotDelivered = "not_delivered" // paid, nothing came
	WrongItem    = "wrong_item"    // paid, something else came
)

// Parties that can bear a disputed payment.
const (
	Platform  = "platform"        // whoever ran the authorization (warrant's operator, the PA)
	User      = "user"            // the account holder
	Merchant  = "merchant"        // through its refund process
	Developer = "agent_developer" // the company whose agent key signed it
	Nobody    = "none"            // no money moved
)

// Ruling is who bears a disputed debit, and why.
type Ruling struct {
	Claim    string   `json:"claim"`
	Liable   string   `json:"liable"`
	Refund   bool     `json:"refund"` // does the user get the money back
	Verified bool     `json:"verified"`
	Why      []string `json:"why"`
}

// Rule applies warrant's liability allocation to a verified pack.
//
// NPCI has not published who bears an agent's mistake. This is a proposal,
// written down so it can be argued with: whoever had the last chance to
// stop a payment, and the evidence to know they should, carries it.
//
//   - The platform carries anything it cannot prove, anything it paid
//     against its own policy, and anything paid after a revocation.
//   - The agent developer carries payments signed with a key it has
//     reported leaked, from the moment it says the leak began.
//   - The merchant carries goods not delivered or not as ordered, through
//     the refund process it already runs.
//   - The user carries what they approved: a step-up they signed for this
//     exact request, an errand their device vouched for, or a payment
//     inside the standing scope they signed that tripped no check.
func Rule(rep Report, claim string) Ruling {
	r := Ruling{Claim: claim, Verified: rep.OK}
	why := func(f string, a ...any) { r.Why = append(r.Why, fmt.Sprintf(f, a...)) }
	if !rep.OK {
		r.Liable, r.Refund = Platform, true
		for _, c := range rep.Checks {
			if !c.OK {
				why("evidence fails: %s (%s)", c.Name, c.Detail)
			}
		}
		why("the platform cannot show the payment was authorized, so it carries it")
		return r
	}
	f := rep.Facts
	if f.DebitStatus != "succeeded" {
		r.Liable = Nobody
		why("the debit %s; no money moved", f.DebitStatus)
		return r
	}
	if !sameDecision(f.Recomputed, f.Decision) {
		r.Liable, r.Refund = Platform, true
		why("the recorded decision (%s) is not what the policy gives (%s)", f.Decision.Outcome, f.Recomputed.Outcome)
		return r
	}
	switch f.Decision.Outcome {
	case policy.Deny:
		r.Liable, r.Refund = Platform, true
		why("the platform paid a request its own policy denied")
		return r
	case policy.StepUp:
		if f.StepUp == nil || !f.StepUp.Approve {
			r.Liable, r.Refund = Platform, true
			why("the platform paid a request that needed the user, without a signed approval")
			return r
		}
	}
	if f.RevokedBeforeDecision {
		r.Liable, r.Refund = Platform, true
		why("the user revoked the mandate before this request was decided")
		return r
	}
	if f.CompromisedSince > 0 && f.RequestTS >= f.CompromisedSince {
		r.Liable, r.Refund = Developer, true
		why("%s reported agent %s's key leaked from %s; this request was signed after that",
			f.Developer, f.Agent, time.UnixMilli(f.CompromisedSince).UTC().Format(time.RFC3339))
		return r
	}
	switch claim {
	case NotDelivered, WrongItem:
		r.Liable, r.Refund = Merchant, true
		why("the payment was authorized; whether the goods arrived as ordered is %s's to make right through its refund process", f.Request.Merchant)
		return r
	}
	r.Liable = User
	switch {
	case f.StepUp != nil:
		why("the user's device signed an approval of this exact request at %s", time.UnixMilli(f.StepUp.TS).UTC().Format(time.RFC3339))
	case f.CoveredByIntent:
		why("the user's device signed intent %q, and this request was inside it", f.Intent.Text)
	default:
		why("the request was inside the standing mandate the user signed (%s) and tripped no check", describe(f))
	}
	return r
}

func describe(f *Facts) string {
	s := fmt.Sprintf("%s at %v, up to %s a debit", f.Scope.Kind, f.Scope.Merchants, wire.Rupees(f.Scope.MaxDebit))
	if f.Scope.Purpose != "" {
		s += fmt.Sprintf(", for %q", f.Scope.Purpose)
	}
	return s
}
