// Package policy decides what happens to an agent's debit request: approve
// it, ask the user (step up), or deny it.
//
// Evaluate is a pure function of its Input, so the same decision can be
// recomputed later from the evidence alone, by anyone, which is what makes
// a dispute about it settleable.
//
// Checks come in two strengths. A hard check fails when the request is
// outside what the user signed or the rail allows: those are denied, since
// no answer from the user could make them valid as they stand. A soft
// check fails when the request is inside the scope but looks unlike what
// the user meant: a gift card in a grocery cart, a payment much larger than
// usual, five payments just under the step-up line. Those go to the user,
// the same way UPI Circle's partial delegation does.
package policy

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/Rakshit-gen/warrant/wire"
)

// Outcomes.
const (
	Approve = "approve"
	StepUp  = "step_up"
	Deny    = "deny"
)

// Reason codes. Hard ones deny, soft ones step up.
const (
	// identity and freshness
	AgentUnknown   = "agent_unknown"
	AgentSuspended = "agent_suspended"
	AgentMismatch  = "agent_not_on_mandate"
	Stale          = "stale_request"
	// the mandate
	MandateInactive = "mandate_inactive"
	MandateExpired  = "mandate_expired"
	NotYetValid     = "mandate_not_yet_valid"
	MerchantOff     = "merchant_not_on_mandate"
	PayeeMismatch   = "payee_not_merchant"
	OverMaxDebit    = "over_max_debit"
	OverRemaining   = "over_remaining"
	OverPerDay      = "over_per_day"
	CoolingPeriod   = "circle_cooling_period"
	CartMismatch    = "amount_not_cart_total"
	CategoryOff     = "category_not_on_mandate"
	BadIntent       = "intent_invalid"
	// soft
	HighRiskItem  = "high_risk_item"
	OverStepUp    = "over_step_up_line"
	IntentMiss    = "outside_intent"
	SplitSuspect  = "split_payment"
	AmountOutlier = "unusual_amount"
	Velocity      = "burst"
)

// Reason is one failed check.
type Reason struct {
	Code   string `json:"code"`
	Hard   bool   `json:"hard"`
	Detail string `json:"detail"`
}

// Decision is the outcome and every reason behind it.
type Decision struct {
	Outcome string   `json:"outcome"`
	Reasons []Reason `json:"reasons"`
}

// Has reports whether the decision carries code.
func (d Decision) Has(code string) bool {
	return slices.ContainsFunc(d.Reasons, func(r Reason) bool { return r.Code == code })
}

// Past is an earlier debit on the same mandate that was approved and not
// failed.
type Past struct {
	At       time.Time `json:"at"`
	Merchant string    `json:"merchant"`
	Amount   int64     `json:"amount"`
}

// Input is everything a decision depends on. The server fills it from the
// database; the evidence verifier fills it from a dispute pack.
type Input struct {
	Now time.Time `json:"now"`

	Scope         wire.Scope `json:"scope"`
	MandateStatus string     `json:"mandate_status"` // "active" or otherwise
	LinkedAt      time.Time  `json:"linked_at"`      // when the mandate was created
	Spent         int64      `json:"spent"`          // settled and in flight, this cap period
	SpentToday    int64      `json:"spent_today"`
	DebitsToday   int        `json:"debits_today"`

	AgentKnown  bool   `json:"agent_known"`
	AgentStatus string `json:"agent_status"`
	MerchantVPA string `json:"merchant_vpa"` // registered VPA of the request's merchant

	Req    wire.DebitRequest   `json:"request"`
	Intent *wire.IntentReceipt `json:"intent,omitempty"` // already signature-checked
	// IntentProblem is set when an intent was sent but failed checks
	// before policy (bad signature, wrong user).
	IntentProblem string `json:"intent_problem,omitempty"`

	// Recent debits on this mandate, newest last, at least the last day.
	Recent []Past `json:"recent"`
	// Amounts of earlier debits on this mandate, for the outlier check.
	History []int64 `json:"history"`
}

// Options switch checks off, for comparing against a weaker policy.
type Options struct {
	// CapsOnly keeps identity, mandate status, merchant and amount limits:
	// what a scheme that only bounds authority and size enforces.
	CapsOnly bool
}

// Tunables. They are constants rather than options because changing them
// changes what past evidence means; a new value is a new policy version.
const (
	Version       = 1
	MaxSkew       = 5 * time.Minute
	SplitWindow   = 15 * time.Minute
	BurstWindow   = 10 * time.Minute
	BurstCount    = 4
	OutlierFactor = 3
	OutlierMinN   = 3
)

// Evaluate decides a request.
func Evaluate(in Input, opt Options) Decision {
	var rs []Reason
	hard := func(code, f string, a ...any) { rs = append(rs, Reason{code, true, fmt.Sprintf(f, a...)}) }
	soft := func(code, f string, a ...any) { rs = append(rs, Reason{code, false, fmt.Sprintf(f, a...)}) }
	s, r := in.Scope, in.Req

	// Who is asking, and is the request fresh.
	switch {
	case !in.AgentKnown:
		hard(AgentUnknown, "agent %s is not registered", r.Agent)
	case in.AgentStatus != "active":
		hard(AgentSuspended, "agent %s is %s", r.Agent, in.AgentStatus)
	}
	if r.Agent != s.Agent {
		hard(AgentMismatch, "mandate is for agent %s", s.Agent)
	}
	if skew := in.Now.Sub(time.UnixMilli(r.TS)); skew > MaxSkew || skew < -MaxSkew {
		hard(Stale, "request is %s away from server time", skew.Round(time.Second))
	}

	// The mandate as signed.
	if in.MandateStatus != "active" {
		hard(MandateInactive, "mandate is %s", in.MandateStatus)
	}
	if in.Now.Unix() >= s.Expires {
		hard(MandateExpired, "mandate expired at %s", time.Unix(s.Expires, 0).UTC().Format(time.RFC3339))
	}
	if in.Now.Unix() < s.NotBefore {
		hard(NotYetValid, "mandate starts at %s", time.Unix(s.NotBefore, 0).UTC().Format(time.RFC3339))
	}
	if !slices.Contains(s.Merchants, r.Merchant) {
		hard(MerchantOff, "merchant %s is not on the mandate", r.Merchant)
	}
	if r.Amount <= 0 {
		hard(OverMaxDebit, "amount must be positive")
	}
	if r.Amount > s.MaxDebit {
		hard(OverMaxDebit, "amount %s is over the per-debit limit %s", wire.Rupees(r.Amount), wire.Rupees(s.MaxDebit))
	}
	if in.Spent+r.Amount > s.Cap {
		hard(OverRemaining, "only %s of %s is left", wire.Rupees(max(0, s.Cap-in.Spent)), wire.Rupees(s.Cap))
	}
	if s.MaxPerDay > 0 && in.DebitsToday >= s.MaxPerDay {
		hard(OverPerDay, "already %d debits today, limit %d", in.DebitsToday, s.MaxPerDay)
	}
	if s.Kind == wire.Circle && in.Now.Sub(in.LinkedAt) < 24*time.Hour && in.SpentToday+r.Amount > wire.CircleCoolingDebit {
		hard(CoolingPeriod, "first 24 hours of a circle link allow %s a day", wire.Rupees(wire.CircleCoolingDebit))
	}
	if opt.CapsOnly {
		return decide(rs)
	}

	// Beyond caps: is this the payment the user signed up for.
	if s.Kind == wire.Circle && r.PayeeVPA != in.MerchantVPA {
		hard(PayeeMismatch, "pays %q but %s is registered as %q", r.PayeeVPA, r.Merchant, in.MerchantVPA)
	}
	if total := r.CartTotal(); total != r.Amount {
		hard(CartMismatch, "cart adds up to %s, request is for %s", wire.Rupees(total), wire.Rupees(r.Amount))
	}
	for _, it := range r.Cart {
		if len(s.Categories) > 0 && !slices.Contains(s.Categories, it.Category) {
			hard(CategoryOff, "%q is %s, mandate allows %v", it.Name, it.Category, s.Categories)
		}
	}
	if in.IntentProblem != "" {
		hard(BadIntent, "%s", in.IntentProblem)
	}
	covered := in.Intent != nil && in.Intent.Covers(r, in.Now)
	if in.Intent != nil && !covered {
		soft(IntentMiss, "request goes beyond intent %q", in.Intent.Text)
	}

	for _, it := range r.Cart {
		if slices.Contains(wire.HighRisk, it.Category) && !slices.Contains(s.AllowHighRisk, it.Category) {
			soft(HighRiskItem, "%q is a %s", it.Name, it.Category)
		}
	}
	if !covered {
		if s.StepUpAbove > 0 && r.Amount > s.StepUpAbove {
			soft(OverStepUp, "%s is over the %s step-up line", wire.Rupees(r.Amount), wire.Rupees(s.StepUpAbove))
		}
		if m, ok := median(in.History); ok && len(in.History) >= OutlierMinN && r.Amount > OutlierFactor*m {
			soft(AmountOutlier, "%s is over %dx the usual %s", wire.Rupees(r.Amount), OutlierFactor, wire.Rupees(m))
		}
		// Several payments to one merchant, each under the line, adding up
		// to more than it.
		var sum int64
		n := 0
		for _, p := range in.Recent {
			if p.Merchant == r.Merchant && in.Now.Sub(p.At) <= SplitWindow {
				sum += p.Amount
				n++
			}
		}
		if s.StepUpAbove > 0 && n > 0 && r.Amount <= s.StepUpAbove && sum+r.Amount > s.StepUpAbove {
			soft(SplitSuspect, "%d payments to %s in %s add up to %s", n+1, r.Merchant, SplitWindow, wire.Rupees(sum+r.Amount))
		}
	}
	burst := 0
	for _, p := range in.Recent {
		if in.Now.Sub(p.At) <= BurstWindow {
			burst++
		}
	}
	if burst >= BurstCount {
		soft(Velocity, "%d debits in the last %s", burst, BurstWindow)
	}
	return decide(rs)
}

func decide(rs []Reason) Decision {
	d := Decision{Outcome: Approve, Reasons: rs}
	for _, r := range rs {
		if r.Hard {
			d.Outcome = Deny
			return d
		}
		d.Outcome = StepUp
	}
	if d.Reasons == nil {
		d.Reasons = []Reason{}
	}
	return d
}

func median(xs []int64) (int64, bool) {
	if len(xs) == 0 {
		return 0, false
	}
	s := slices.Clone(xs)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	if len(s)%2 == 1 {
		return s[len(s)/2], true
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2, true
}
