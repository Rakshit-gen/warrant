package server_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Rakshit-gen/warrant/bank"
	"github.com/Rakshit-gen/warrant/client"
	"github.com/Rakshit-gen/warrant/evidence"
	"github.com/Rakshit-gen/warrant/policy"
	"github.com/Rakshit-gen/warrant/server"
	"github.com/Rakshit-gen/warrant/sign"
	"github.com/Rakshit-gen/warrant/wire"
)

// These tests need a Postgres they may wipe: WARRANT_TEST_DB, for example
// the one scripts/devdb.sh start prints.

type env struct {
	s      *server.Server
	sim    *bank.Sim
	api    *client.API
	db     *pgxpool.Pool
	device client.Device
	agent  client.Agent
}

func setup(t *testing.T, opt policy.Options) *env {
	t.Helper()
	url := os.Getenv("WARRANT_TEST_DB")
	if url == "" {
		t.Skip("WARRANT_TEST_DB is not set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	sim := bank.NewSim(bank.Faults{}, 7)
	s, err := server.New(ctx, server.Config{DB: db, Rail: sim, Key: sign.NewKey(), Policy: opt})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(s.Handler())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; hs.Close(); db.Close() })

	api := client.New(hs.URL)
	e := &env{s: s, sim: sim, api: api, db: db,
		device: client.Device{API: api, User: "asha", Key: sign.NewKey()},
		agent:  client.Agent{API: api, ID: "shopbot", Key: sign.NewKey()}}
	must(t, api.AddUser(ctx, "asha", "Asha", e.device.Key))
	must(t, api.AddAgent(ctx, "shopbot", "ShopBot", "acme-ai", e.agent.Key))
	must(t, api.AddMerchant(ctx, "zepto", "Zepto", "zepto@ybl"))
	must(t, api.AddMerchant(ctx, "blinkit", "Blinkit", "blinkit@hdfc"))
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func groceries(n int, price int64) []wire.Item {
	return []wire.Item{{SKU: "atta-5kg", Name: "Atta 5kg", Category: "grocery", Qty: n, Price: price}}
}

func pay(t *testing.T, e *env, merchant string, cart []wire.Item, intent *sign.Envelope, intentID string) client.Result {
	t.Helper()
	req := wire.DebitRequest{Mandate: "m1", Merchant: merchant, Cart: cart, IntentID: intentID}
	for _, it := range cart {
		req.Amount += int64(it.Qty) * it.Price
	}
	res, err := e.agent.Pay(context.Background(), req, intent)
	must(t, err)
	return res
}

func TestAgentPaymentFlow(t *testing.T) {
	e := setup(t, policy.Options{})
	ctx := context.Background()
	now := time.Now()
	m, err := e.device.Grant(ctx, wire.Scope{ID: "m1", Agent: "shopbot", Kind: wire.ReservePay, Merchants: []string{"zepto"},
		Categories: []string{"grocery", "snacks"}, MaxDebit: 3000_00, Cap: 10000_00, StepUpAbove: 2000_00,
		Purpose: "weekly groceries", NotBefore: now.Add(-time.Minute).Unix(), Expires: now.Add(30 * 24 * time.Hour).Unix()})
	must(t, err)
	if m.BlockRef == "" || m.Status != "active" {
		t.Fatalf("mandate %+v", m)
	}

	// Inside the scope: paid.
	a := pay(t, e, "zepto", groceries(2, 250_00), nil, "")
	if a.Outcome != policy.Approve || a.Debit == nil || a.Debit.Status != "succeeded" {
		t.Fatalf("plain grocery order: %+v", a)
	}

	// The same signed request again is the same answer, not a second payment.
	req := wire.DebitRequest{Mandate: "m1", Merchant: "zepto", Amount: 100_00, Cart: groceries(1, 100_00), Nonce: "n-fixed", IdemKey: "errand-1"}
	req.Type, req.Agent, req.TS = "debit", "shopbot", time.Now().UnixMilli()
	signed, _ := e.agent.Key.Seal(req)
	r1, err := e.agent.Send(ctx, signed, nil)
	must(t, err)
	r2, err := e.agent.Send(ctx, signed, nil)
	must(t, err)
	if !r2.Duplicate || r2.Debit == nil || r1.Debit.ID != r2.Debit.ID {
		t.Fatalf("retry was not deduplicated: %+v / %+v", r1, r2)
	}
	// The same nonce under a new idempotency key is a replay.
	req.IdemKey = "errand-2"
	replayed, _ := e.agent.Key.Seal(req)
	if _, err := e.agent.Send(ctx, replayed, nil); !isStatus(err, 409) {
		t.Fatalf("replay: %v", err)
	}

	// The bank moves the money and the reply is lost: warrant does not know,
	// asks, and settles it once.
	e.sim.SetFaults(bank.Faults{LostReply: 1})
	lost := pay(t, e, "zepto", groceries(1, 400_00), nil, "")
	e.sim.SetFaults(bank.Faults{})
	if lost.Debit == nil || lost.Debit.Status != "unknown" {
		t.Fatalf("lost reply: %+v", lost)
	}
	waitDebit(t, e, lost.Request, "succeeded")

	// A gift card on a groceries mandate: refused outright.
	gc := pay(t, e, "zepto", []wire.Item{{SKU: "gc", Name: "Gift card", Category: "gift_card", Qty: 1, Price: 1000_00}}, nil, "")
	if gc.Outcome != policy.Deny || !gc.Has(policy.CategoryOff) {
		t.Fatalf("gift card: %+v", gc)
	}

	// An errand the user's device vouched for: it may go past the usual
	// amount checks, but only inside what the device signed.
	intent, err := e.device.Intent(wire.IntentReceipt{ID: "int-1", Agent: "shopbot", Mandate: "m1", Text: "snacks for the match tonight",
		Merchants: []string{"zepto"}, Categories: []string{"snacks"}, MaxAmount: 800_00, Expires: now.Add(time.Hour).Unix()})
	must(t, err)
	snacks := pay(t, e, "zepto", []wire.Item{{SKU: "chips", Name: "Chips", Category: "snacks", Qty: 7, Price: 100_00}}, &intent, "int-1")
	if snacks.Outcome != policy.Approve || snacks.Debit.Status != "succeeded" {
		t.Fatalf("intent errand: %+v", snacks)
	}

	// Over the step-up line, and a burst besides: the user is asked, says
	// yes, it is paid.
	big := pay(t, e, "zepto", groceries(1, 2500_00), nil, "")
	if big.Outcome != policy.StepUp || !big.Has(policy.OverStepUp) || big.StepUp == nil {
		t.Fatalf("big order: %+v", big)
	}
	pending, err := e.device.Pending(ctx)
	must(t, err)
	if len(pending) != 1 || pending[0].ID != *big.StepUp {
		t.Fatalf("pending step-ups: %+v", pending)
	}
	if _, err := e.device.Answer(ctx, pending[0], true); err != nil {
		t.Fatal(err)
	}
	bigDone := waitDebit(t, e, big.Request, "succeeded")

	// Another small one, sixth in ten minutes, looks like a runaway agent:
	// asked, and the user says no.
	burst := pay(t, e, "zepto", groceries(1, 50_00), nil, "")
	if burst.Outcome != policy.StepUp || !burst.Has(policy.Velocity) {
		t.Fatalf("burst: %+v", burst)
	}
	pending, _ = e.device.Pending(ctx)
	must(t, errOf(e.device.Answer(ctx, pending[0], false)))
	if r, _ := e.api.Poll(ctx, burst.Request); r.Outcome != policy.Deny || r.Debit != nil {
		t.Fatalf("declined step-up: %+v", r)
	}

	// Disputes, from the evidence alone.
	rule := func(debit, claim, want string) evidence.Ruling {
		t.Helper()
		p, err := e.api.Evidence(ctx, debit)
		must(t, err)
		if rep := evidence.Verify(p); !rep.OK {
			t.Fatalf("pack for %s does not verify: %+v", debit, rep.Checks)
		}
		r, err := e.api.Dispute(ctx, debit, claim)
		must(t, err)
		if r.Liable != want {
			t.Fatalf("%s on %s: liable %s, want %s (%v)", claim, debit, r.Liable, want, r.Why)
		}
		return r
	}
	rule(a.Debit.ID, evidence.NotDelivered, evidence.Merchant)
	rule(a.Debit.ID, evidence.Unauthorized, evidence.User)
	rule(bigDone.ID, evidence.Unauthorized, evidence.User)
	rule(snacks.Debit.ID, evidence.Unauthorized, evidence.User)

	// A pack someone edited no longer verifies, and the platform carries it.
	p, _ := e.api.Evidence(ctx, a.Debit.ID)
	for i := range p.Entries {
		if p.Entries[i].Kind == "mandate" {
			p.Entries[i].Body = []byte(string(p.Entries[i].Body[:len(p.Entries[i].Body)-1]) + ` `)
		}
	}
	if rep := evidence.Verify(p); rep.OK || evidence.Rule(rep, evidence.Unauthorized).Liable != evidence.Platform {
		t.Fatal("edited pack still verifies")
	}

	// Strangers and forgeries never reach policy.
	stranger := client.Agent{API: e.api, ID: "shopbot", Key: sign.NewKey()}
	if _, err := stranger.Pay(ctx, wire.DebitRequest{Mandate: "m1", Merchant: "zepto", Amount: 1, Cart: groceries(1, 1)}, nil); !isStatus(err, 401) {
		t.Fatalf("forged signature: %v", err)
	}
	ghost := client.Agent{API: e.api, ID: "ghost", Key: sign.NewKey()}
	if _, err := ghost.Pay(ctx, wire.DebitRequest{Mandate: "m1", Merchant: "zepto", Amount: 1, Cart: groceries(1, 1)}, nil); !isStatus(err, 401) {
		t.Fatalf("unregistered agent: %v", err)
	}

	// UPI Circle: the agent pays from the user's account, so the payee VPA
	// has to be the merchant's own.
	_, err = e.device.Grant(ctx, wire.Scope{ID: "c1", Agent: "shopbot", Kind: wire.Circle, Merchants: []string{"blinkit"},
		MaxDebit: 2000_00, Cap: 5000_00, NotBefore: now.Add(-time.Minute).Unix(), Expires: now.Add(30 * 24 * time.Hour).Unix()})
	must(t, err)
	swap, err := e.agent.Pay(ctx, wire.DebitRequest{Mandate: "c1", Merchant: "blinkit", PayeeVPA: "blinkit.refunds@okaxis", Amount: 300_00, Cart: groceries(1, 300_00)}, nil)
	must(t, err)
	if swap.Outcome != policy.Deny || !swap.Has(policy.PayeeMismatch) {
		t.Fatalf("payee swap: %+v", swap)
	}

	// Revoked means nothing more goes through.
	must(t, e.device.Revoke(ctx, "m1"))
	after := pay(t, e, "zepto", groceries(1, 100_00), nil, "")
	if after.Outcome != policy.Deny || !after.Has(policy.MandateInactive) {
		t.Fatalf("after revoke: %+v", after)
	}

	// The developer reports the agent's key leaked from before the first
	// order: that order is now theirs to refund.
	must(t, e.api.SuspendAgent(ctx, "shopbot", "key found in a public repo", now.Add(-time.Minute)))
	rule(a.Debit.ID, evidence.Unauthorized, evidence.Developer)

	// Every rupee the bank moved is a debit warrant recorded as succeeded.
	checkBooks(t, e)
}

// TestNoOverspendUnderRace fires many approvals at one mandate at once,
// through a bank that drops and loses replies, and checks the books.
func TestNoOverspendUnderRace(t *testing.T) {
	e := setup(t, policy.Options{CapsOnly: true})
	ctx := context.Background()
	now := time.Now()
	_, err := e.device.Grant(ctx, wire.Scope{ID: "m1", Agent: "shopbot", Kind: wire.ReservePay, Merchants: []string{"zepto"},
		MaxDebit: 1000_00, Cap: 5000_00, NotBefore: now.Add(-time.Minute).Unix(), Expires: now.Add(24 * time.Hour).Unix()})
	must(t, err)
	e.sim.SetFaults(bank.Faults{Jitter: 20 * time.Millisecond, Drop: 0.1, LostReply: 0.3})

	var wg sync.WaitGroup
	results := make([]client.Result, 40)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = pay(t, e, "zepto", groceries(1, 300_00), nil, "")
		}()
	}
	wg.Wait()
	approved := 0
	for _, r := range results {
		if r.Outcome == policy.Approve {
			approved++
		}
	}
	// 5000 / 300 = 16 fit; the rest must be refused at decision time.
	if approved != 16 {
		t.Fatalf("%d approved, want 16", approved)
	}
	e.sim.SetFaults(bank.Faults{})
	deadline := time.Now().Add(20 * time.Second)
	for {
		var open int
		must(t, e.db.QueryRow(ctx, `SELECT count(*) FROM debits WHERE status IN ('pending', 'unknown')`).Scan(&open))
		if open == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d debits still open", open)
		}
		time.Sleep(200 * time.Millisecond)
	}
	checkBooks(t, e)
}

// checkBooks compares warrant's ledger with the bank's.
func checkBooks(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	rows, err := e.db.Query(ctx, `SELECT m.block_ref, coalesce(sum(d.amount) FILTER (WHERE d.status = 'succeeded'), 0), count(*) FILTER (WHERE d.status = 'succeeded')
		FROM mandates m LEFT JOIN debits d ON d.mandate_id = m.id GROUP BY m.id`)
	must(t, err)
	defer rows.Close()
	audit := e.sim.Audit()
	ours := 0
	for rows.Next() {
		var block string
		var sum int64
		var n int
		must(t, rows.Scan(&block, &sum, &n))
		if audit.Debited[block] != sum {
			t.Errorf("block %s: bank debited %d, warrant recorded %d", block, audit.Debited[block], sum)
		}
		ours += n
	}
	if audit.Txns != ours {
		t.Errorf("bank has %d successful debits, warrant %d", audit.Txns, ours)
	}
	var cap int64
	must(t, e.db.QueryRow(ctx, `SELECT (convert_from(decode(scope->>'payload', 'base64'), 'UTF8')::jsonb->>'cap')::bigint FROM mandates WHERE id = 'm1'`).Scan(&cap))
	if d := audit.Debited[blockOf(t, e, "m1")]; d > cap {
		t.Errorf("m1 overspent: %d of %d", d, cap)
	}
}

func blockOf(t *testing.T, e *env, mandate string) string {
	var b string
	must(t, e.db.QueryRow(context.Background(), `SELECT block_ref FROM mandates WHERE id = $1`, mandate).Scan(&b))
	return b
}

func waitDebit(t *testing.T, e *env, request, status string) client.Debit {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r, err := e.api.Poll(context.Background(), request)
		must(t, err)
		if r.Debit != nil && r.Debit.Status == status {
			return *r.Debit
		}
		if time.Now().After(deadline) {
			t.Fatalf("request %s: debit %+v, want %s", request, r.Debit, status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func isStatus(err error, code int) bool {
	var e *client.Error
	return errors.As(err, &e) && e.Status == code
}

func errOf(_ any, err error) error { return err }
