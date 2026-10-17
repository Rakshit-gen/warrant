package policy

import (
	"testing"
	"time"

	"github.com/Rakshit-gen/warrant/wire"
)

var now = time.Date(2026, 10, 18, 19, 30, 0, 0, time.UTC)

func base() Input {
	return Input{
		Now: now,
		Scope: wire.Scope{
			Type: "scope", ID: "m1", User: "u1", Agent: "a1", Kind: wire.ReservePay,
			Merchants: []string{"zepto"}, Categories: []string{"grocery", "snacks"},
			MaxDebit: 2000_00, Cap: 10000_00, StepUpAbove: 1000_00,
			NotBefore: now.Add(-24 * time.Hour).Unix(), Expires: now.Add(30 * 24 * time.Hour).Unix(),
		},
		MandateStatus: "active",
		LinkedAt:      now.Add(-24 * time.Hour),
		AgentKnown:    true,
		AgentStatus:   "active",
		MerchantVPA:   "zepto@axis",
		Req: wire.DebitRequest{
			Type: "debit", Mandate: "m1", Agent: "a1", Nonce: "n", TS: now.UnixMilli(), IdemKey: "k",
			Merchant: "zepto", Amount: 450_00,
			Cart: []wire.Item{{SKU: "milk", Name: "Milk 1L", Category: "grocery", Qty: 3, Price: 70_00}, {SKU: "chips", Name: "Chips", Category: "snacks", Qty: 4, Price: 60_00}},
		},
		History: []int64{400_00, 520_00, 380_00, 610_00},
	}
}

func TestDecisions(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(*Input)
		outcome string
		code    string
	}{
		{"usual reorder", func(*Input) {}, Approve, ""},
		{"unregistered agent", func(in *Input) { in.AgentKnown = false }, Deny, AgentUnknown},
		{"another agent", func(in *Input) { in.Req.Agent = "a2" }, Deny, AgentMismatch},
		{"old request", func(in *Input) { in.Req.TS = now.Add(-time.Hour).UnixMilli() }, Deny, Stale},
		{"revoked", func(in *Input) { in.MandateStatus = "revoked" }, Deny, MandateInactive},
		{"expired", func(in *Input) { in.Now = time.Unix(in.Scope.Expires, 0) }, Deny, MandateExpired},
		{"other merchant", func(in *Input) { in.Req.Merchant = "zepto-deals" }, Deny, MerchantOff},
		{"over per-debit", func(in *Input) { setCart(in, "grocery", 2500_00) }, Deny, OverMaxDebit},
		{"over remaining", func(in *Input) { in.Spent = 9800_00 }, Deny, OverRemaining},
		{"hidden fee", func(in *Input) { in.Req.Amount += 99_00 }, Deny, CartMismatch},
		{"category outside scope", func(in *Input) { in.Req.Cart[1].Category = "alcohol" }, Deny, CategoryOff},
		{"forged intent", func(in *Input) { in.IntentProblem = "signature does not verify" }, Deny, BadIntent},
		{"gift card slipped in", func(in *Input) {
			in.Scope.Categories = nil
			in.Req.Cart = append(in.Req.Cart, wire.Item{Name: "Gift card", Category: "gift_card", Qty: 1, Price: 500_00})
			in.Req.Amount += 500_00
		}, StepUp, HighRiskItem},
		{"big order", func(in *Input) { setCart(in, "grocery", 1800_00) }, StepUp, OverStepUp},
		{"big order with intent", func(in *Input) {
			setCart(in, "grocery", 1800_00)
			in.Intent = &wire.IntentReceipt{Mandate: "m1", Agent: "a1", MaxAmount: 2000_00, Categories: []string{"grocery"}, Expires: now.Add(time.Hour).Unix()}
		}, Approve, ""},
		{"intent overreach", func(in *Input) {
			in.Intent = &wire.IntentReceipt{Mandate: "m1", Agent: "a1", MaxAmount: 300_00, Expires: now.Add(time.Hour).Unix()}
		}, StepUp, IntentMiss},
		{"outlier", func(in *Input) {
			in.Scope.StepUpAbove = 0
			setCart(in, "grocery", 1900_00)
		}, StepUp, AmountOutlier},
		{"split under the line", func(in *Input) {
			setCart(in, "grocery", 900_00)
			in.Recent = []Past{{At: now.Add(-5 * time.Minute), Merchant: "zepto", Amount: 950_00}}
		}, StepUp, SplitSuspect},
		{"burst", func(in *Input) {
			for i := range 4 {
				in.Recent = append(in.Recent, Past{At: now.Add(-time.Duration(i+1) * time.Minute), Merchant: "other", Amount: 10_00})
			}
		}, StepUp, Velocity},
		{"circle payee swap", func(in *Input) {
			in.Scope.Kind = wire.Circle
			in.Req.PayeeVPA = "refund.desk@ybl"
		}, Deny, PayeeMismatch},
		{"circle cooling", func(in *Input) {
			in.Scope.Kind = wire.Circle
			in.Req.PayeeVPA = "zepto@axis"
			in.LinkedAt = now.Add(-time.Hour)
			in.SpentToday = 4800_00
		}, Deny, CoolingPeriod},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base()
			c.edit(&in)
			d := Evaluate(in, Options{})
			if d.Outcome != c.outcome || (c.code != "" && !d.Has(c.code)) {
				t.Fatalf("got %s %+v, want %s with %s", d.Outcome, d.Reasons, c.outcome, c.code)
			}
		})
	}
}

func TestCapsOnlyMissesWhatScopeCatches(t *testing.T) {
	in := base()
	in.Req.Cart[1].Category = "alcohol"
	if d := Evaluate(in, Options{CapsOnly: true}); d.Outcome != Approve {
		t.Fatalf("caps-only should not look at categories, got %+v", d)
	}
	if d := Evaluate(in, Options{}); d.Outcome != Deny {
		t.Fatalf("full policy should deny, got %+v", d)
	}
}

func setCart(in *Input, cat string, amount int64) {
	in.Req.Cart = []wire.Item{{SKU: "x", Name: "Order", Category: cat, Qty: 1, Price: amount}}
	in.Req.Amount = amount
}
