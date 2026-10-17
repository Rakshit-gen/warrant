// Package wire defines the signed documents of an agent payment.
//
// A user signs a Scope once, with the device key their UPI app holds. That
// is the strong, user-present moment. Afterwards an agent pays inside the
// scope by signing DebitRequests with its own registered key. For a session
// ("order snacks for the match"), the user's device can also sign an
// IntentReceipt, a short-lived statement of what this errand is for. When
// warrant needs a human, the user signs a StepUp answer; and a Revocation
// ends a mandate.
//
// All money is in paise.
package wire

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Mandate kinds, after the UPI features they ride on.
const (
	// ReservePay is UPI's single-block-multiple-debit: funds are blocked
	// once for one merchant and debited in parts.
	ReservePay = "reserve_pay"
	// Circle is UPI Circle's full delegation: the agent is a secondary user
	// paying from the user's account within delegated limits.
	Circle = "circle"
)

// Limits NPCI sets on the rails underneath. A scope may be tighter, never
// looser.
const (
	ReservePayMaxBlock   = 10_000_00 // per block
	ReservePayMaxDays    = 90
	CircleMaxDebit       = 5_000_00  // per payment
	CircleMaxMonth       = 15_000_00 // per calendar month
	CircleCoolingDebit   = 5_000_00  // per day, first 24 hours after linking
	ReservePayMaxRetries = 3         // attempts at one debit within 24 hours
)

// Scope is what a user lets one agent do. The user's signature over it is
// the consent every later debit leans on.
type Scope struct {
	Type      string   `json:"type"` // "scope"
	ID        string   `json:"id"`   // mandate ID, chosen by the user's app
	User      string   `json:"user"`
	Agent     string   `json:"agent"`
	Kind      string   `json:"kind"`
	Merchants []string `json:"merchants"`
	// Categories of goods the agent may buy. Empty means any.
	Categories []string `json:"categories,omitempty"`
	// High-risk categories (gift cards, top-ups...) the user explicitly
	// allows without a step-up.
	AllowHighRisk []string `json:"allow_high_risk,omitempty"`
	MaxDebit      int64    `json:"max_debit"`
	Cap           int64    `json:"cap"` // whole block (reserve_pay) or per month (circle)
	MaxPerDay     int      `json:"max_per_day,omitempty"`
	// Debits above this need the user, unless an intent receipt covers them.
	StepUpAbove int64  `json:"step_up_above"`
	Purpose     string `json:"purpose,omitempty"` // the standing instruction, in the user's words
	NotBefore   int64  `json:"not_before"`        // unix seconds
	Expires     int64  `json:"expires"`
}

// Validate checks a scope against itself and the rail's limits.
func (s Scope) Validate() error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }
	if s.Type != "scope" {
		bad("type is %q, want scope", s.Type)
	}
	if s.ID == "" || s.User == "" || s.Agent == "" {
		bad("id, user and agent are required")
	}
	if len(s.Merchants) == 0 {
		bad("at least one merchant is required")
	}
	if s.MaxDebit <= 0 || s.Cap <= 0 || s.MaxDebit > s.Cap {
		bad("need 0 < max_debit <= cap")
	}
	if s.StepUpAbove < 0 {
		bad("step_up_above is negative")
	}
	if s.Expires <= s.NotBefore {
		bad("expires must be after not_before")
	}
	switch s.Kind {
	case ReservePay:
		if len(s.Merchants) != 1 {
			bad("a reserve_pay block is for exactly one merchant")
		}
		if s.Cap > ReservePayMaxBlock {
			bad("cap %d is over the reserve_pay block limit %d", s.Cap, ReservePayMaxBlock)
		}
		if time.Duration(s.Expires-s.NotBefore)*time.Second > ReservePayMaxDays*24*time.Hour {
			bad("reserve_pay blocks last at most %d days", ReservePayMaxDays)
		}
	case Circle:
		if s.MaxDebit > CircleMaxDebit {
			bad("max_debit %d is over the circle per-payment limit %d", s.MaxDebit, CircleMaxDebit)
		}
		if s.Cap > CircleMaxMonth {
			bad("cap %d is over the circle monthly limit %d", s.Cap, CircleMaxMonth)
		}
	default:
		bad("kind %q is not reserve_pay or circle", s.Kind)
	}
	return errors.Join(errs...)
}

// Item is one line of a cart.
type Item struct {
	SKU      string `json:"sku"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Qty      int    `json:"qty"`
	Price    int64  `json:"price"` // per unit
}

// DebitRequest is an agent asking to pay. The agent signs it.
type DebitRequest struct {
	Type     string `json:"type"` // "debit"
	Mandate  string `json:"mandate"`
	Agent    string `json:"agent"`
	Nonce    string `json:"nonce"`
	TS       int64  `json:"ts"`       // unix milliseconds
	IdemKey  string `json:"idem_key"` // the same errand retried keeps its key
	Merchant string `json:"merchant"`
	PayeeVPA string `json:"payee_vpa,omitempty"` // circle only; reserve_pay pays the block's merchant
	Amount   int64  `json:"amount"`
	Cart     []Item `json:"cart"`
	// IntentID points at an intent receipt sent alongside, if any.
	IntentID string `json:"intent_id,omitempty"`
	Note     string `json:"note,omitempty"`
}

// CartTotal is what the cart adds up to.
func (r DebitRequest) CartTotal() int64 {
	var t int64
	for _, it := range r.Cart {
		t += int64(it.Qty) * it.Price
	}
	return t
}

// IntentReceipt is the user's device vouching for one errand: what it is
// for, where, and how much. It narrows the scope; it never widens it.
type IntentReceipt struct {
	Type       string   `json:"type"` // "intent"
	ID         string   `json:"id"`
	User       string   `json:"user"`
	Agent      string   `json:"agent"`
	Mandate    string   `json:"mandate"`
	Text       string   `json:"text"` // what the user asked for
	Merchants  []string `json:"merchants,omitempty"`
	Categories []string `json:"categories,omitempty"`
	MaxAmount  int64    `json:"max_amount"`
	Expires    int64    `json:"expires"` // unix seconds
}

// Covers says whether the receipt vouches for this request.
func (ir IntentReceipt) Covers(r DebitRequest, now time.Time) bool {
	if ir.Mandate != r.Mandate || ir.Agent != r.Agent || now.Unix() > ir.Expires {
		return false
	}
	if r.Amount > ir.MaxAmount {
		return false
	}
	if len(ir.Merchants) > 0 && !slices.Contains(ir.Merchants, r.Merchant) {
		return false
	}
	if len(ir.Categories) > 0 {
		for _, it := range r.Cart {
			if !slices.Contains(ir.Categories, it.Category) {
				return false
			}
		}
	}
	return true
}

// StepUp is the user's answer when warrant asks about a request.
type StepUp struct {
	Type    string `json:"type"`    // "stepup"
	ID      string `json:"id"`      // the step-up being answered
	Request string `json:"request"` // digest of the signed debit request
	Approve bool   `json:"approve"`
	TS      int64  `json:"ts"` // unix milliseconds
}

// Revocation ends a mandate.
type Revocation struct {
	Type    string `json:"type"` // "revoke"
	Mandate string `json:"mandate"`
	TS      int64  `json:"ts"`
}

// HighRisk are categories that turn into cash or leave the merchant's
// world: what an injected instruction most often tries to buy.
var HighRisk = []string{"gift_card", "wallet_topup", "prepaid", "crypto", "money_transfer", "lottery"}

// Rupees formats paise the Indian way: ₹1,00,000 and ₹2,095.50.
func Rupees(paise int64) string {
	sign := ""
	if paise < 0 {
		sign, paise = "-", -paise
	}
	s := fmt.Sprint(paise / 100)
	if len(s) > 3 {
		head, tail := s[:len(s)-3], s[len(s)-3:]
		var groups []string
		for len(head) > 2 {
			groups = append([]string{head[len(head)-2:]}, groups...)
			head = head[:len(head)-2]
		}
		groups = append([]string{head}, groups...)
		s = strings.Join(groups, ",") + "," + tail
	}
	if paise%100 != 0 {
		s += fmt.Sprintf(".%02d", paise%100)
	}
	return sign + "₹" + s
}
