// Package eval replays agent shopping sessions through a running warrant
// and counts what got paid.
//
// The corpus is synthetic. Each scenario is one user, one agent, one
// mandate and a few weeks of orders, generated from a seed. Benign
// scenarios are what a shopping agent normally does. Attack scenarios are
// what an agent that was prompt-injected, or whose key was stolen, tries
// to do: each family is a pattern from public writing on agent payment
// risk (gift-card injection, payee swaps, split payments, draining a
// mandate). Evasion scenarios are attacks built to slip past warrant's
// checks, kept in so the misses are counted too.
//
// Nothing here is real traffic. The numbers say what the checks catch on
// these patterns, not what share of real fraud they would stop.
package eval

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/Rakshit-gen/warrant/wire"
)

// Kinds of scenario.
const (
	Benign  = "benign"
	Attack  = "attack"
	Evasion = "evasion"
)

// Step acts.
const (
	Pay    = "pay"
	Replay = "replay" // send the last benign signed request again, unchanged
	Forge  = "forge"  // sign with a key that is not the agent's
	Revoke = "revoke" // the user revokes the mandate
)

// Step is one thing that happens in a session.
type Step struct {
	After     time.Duration // clock moves this much before the step
	Act       string
	Merchant  string
	PayeeVPA  string
	Amount    int64 // 0 means the cart total
	Cart      []wire.Item
	Intent    *wire.IntentReceipt // signed by the user's device, sent along
	Malicious bool                // the user did not want this payment
}

// Scenario is one session.
type Scenario struct {
	ID     string
	Family string
	Kind   string
	Scope  wire.Scope // ID, User, Agent, NotBefore and Expires are set by the runner
	Days   int        // how long the mandate lasts
	Steps  []Step
}

// Merchants every scenario can use, with their registered VPAs.
var Merchants = map[string]string{
	"zepto":     "zepto@ybl",
	"blinkit":   "blinkit@hdfcbank",
	"bigbasket": "bigbasket@icici",
	"airtel":    "airtelpayments@airtel",
	"bescom":    "bescom.bills@sbi",
	"zeptoo":    "zeptoo.store@okaxis", // a lookalike that onboarded as a merchant
}

type product struct {
	name, category string
	price          int64 // paise
}

var catalog = map[string][]product{
	"grocery": {{"Atta 5kg", "grocery", 289_00}, {"Toor dal 1kg", "grocery", 165_00}, {"Basmati rice 5kg", "grocery", 649_00},
		{"Milk 1L", "grocery", 68_00}, {"Eggs 12", "grocery", 89_00}, {"Paneer 200g", "grocery", 95_00}, {"Onions 1kg", "grocery", 45_00},
		{"Tomatoes 1kg", "grocery", 40_00}, {"Sunflower oil 1L", "grocery", 189_00}, {"Sugar 1kg", "grocery", 52_00}},
	"snacks": {{"Chips", "snacks", 50_00}, {"Namkeen 400g", "snacks", 120_00}, {"Cola 2L", "snacks", 99_00}, {"Biscuits", "snacks", 40_00},
		{"Chocolates", "snacks", 150_00}, {"Ice cream 1L", "snacks", 249_00}},
	"household": {{"Detergent 2kg", "household", 399_00}, {"Dishwash gel", "household", 250_00}, {"Toilet cleaner", "household", 179_00},
		{"Tissues", "household", 149_00}},
	"electronics": {{"Wireless earbuds", "electronics", 2499_00}, {"Power bank", "electronics", 1799_00}, {"Smart bulb", "electronics", 899_00}},
}

type gen struct {
	r *rand.Rand
}

func (g gen) between(lo, hi int64) int64 { return lo + g.r.Int64N(hi-lo+1) }

func (g gen) days(lo, hi int) time.Duration {
	return time.Duration(lo*24*60+g.r.IntN((hi-lo)*24*60+1)) * time.Minute
}

func (g gen) minutes(lo, hi int) time.Duration {
	return time.Duration(lo*60+g.r.IntN((hi-lo)*60+1)) * time.Second
}

// cart fills a cart from the categories until it lands between lo and hi.
func (g gen) cart(lo, hi int64, cats ...string) []wire.Item {
	for {
		var items []wire.Item
		var total int64
		for total < lo {
			p := catalog[cats[g.r.IntN(len(cats))]]
			it := p[g.r.IntN(len(p))]
			items = append(items, wire.Item{SKU: fmt.Sprintf("sku-%x", g.r.Uint32()), Name: it.name, Category: it.category, Qty: 1, Price: it.price})
			total += it.price
		}
		if total <= hi {
			return items
		}
	}
}

func total(items []wire.Item) int64 {
	var t int64
	for _, it := range items {
		t += int64(it.Qty) * it.Price
	}
	return t
}

// groceryScope is the usual standing mandate: one quick-commerce merchant,
// everyday categories, a step-up line well under the per-debit limit.
func (g gen) groceryScope(merchant string) wire.Scope {
	sc := wire.Scope{Kind: wire.ReservePay, Merchants: []string{merchant}, MaxDebit: 3000_00, Cap: 10000_00,
		StepUpAbove: 1500_00, Purpose: "weekly groceries and household things"}
	// Most users would name categories; some would leave them open.
	if g.r.Float64() < 0.7 {
		sc.Categories = []string{"grocery", "snacks", "household"}
	}
	return sc
}

func billScope() wire.Scope {
	return wire.Scope{Kind: wire.Circle, Merchants: []string{"airtel", "bescom"}, MaxDebit: 2000_00, Cap: 5000_00,
		StepUpAbove: 1500_00, Purpose: "pay my phone and electricity bills"}
}

// history is a few ordinary orders, days apart.
func (g gen) history(merchant string, n int) []Step {
	var s []Step
	for i := range n {
		after := g.days(1, 4)
		if i == 0 {
			after = g.days(1, 2) // and not in a circle link's first day
		}
		s = append(s, Step{After: after, Act: Pay, Merchant: merchant, Cart: g.cart(300_00, 900_00, "grocery", "grocery", "snacks", "household")})
	}
	return s
}

func (g gen) bills(n int) []Step {
	var s []Step
	for range n {
		m := []string{"airtel", "bescom"}[g.r.IntN(2)]
		amt := g.between(399_00, 1400_00)
		s = append(s, Step{After: g.days(25, 32), Act: Pay, Merchant: m, PayeeVPA: Merchants[m], Amount: amt,
			Cart: []wire.Item{{SKU: "bill", Name: "Bill payment", Category: "bill", Qty: 1, Price: amt}}})
	}
	return s
}

func (g gen) merchant() string { return []string{"zepto", "blinkit", "bigbasket"}[g.r.IntN(3)] }

