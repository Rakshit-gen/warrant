package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Rakshit-gen/warrant/bank"
	"github.com/Rakshit-gen/warrant/client"
	"github.com/Rakshit-gen/warrant/evidence"
	"github.com/Rakshit-gen/warrant/policy"
	"github.com/Rakshit-gen/warrant/sign"
	"github.com/Rakshit-gen/warrant/wire"
)

func cmdDemo(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	dbURL := fs.String("db", os.Getenv("WARRANT_DB"), "Postgres URL, for a warrantd run inside this process (schema warrant_demo, dropped first)")
	apiURL := fs.String("api", "", "a running warrantd to use instead, for example http://localhost:8090")
	token := fs.String("token", os.Getenv("WARRANT_ADMIN_TOKEN"), "its admin token, if it has one")
	pack := fs.String("pack", "pack.json", "where to write the evidence pack for the approved big order")
	transcript := fs.String("transcript", "", "also write the story as JSON, for the website")
	fs.Parse(args)

	var api *client.API
	var sim *bank.Sim
	if *apiURL != "" {
		api = client.New(strings.TrimRight(*apiURL, "/"))
		api.Token = *token
	} else {
		h, err := start(ctx, *dbURL, "warrant_demo", policy.Options{}, nil)
		if err != nil {
			return err
		}
		defer h.close()
		api, sim = h.api, h.sim
	}

	run := fmt.Sprintf("%04d", time.Now().UnixMilli()%10000) // keeps IDs apart on a shared warrantd
	asha := client.Device{API: api, User: "asha-" + run, Key: sign.NewKey()}
	bot := client.Agent{API: api, ID: "shopbot-" + run, Key: sign.NewKey()}
	for _, err := range []error{
		api.AddUser(ctx, asha.User, "Asha", asha.Key),
		api.AddAgent(ctx, bot.ID, "ShopBot", "Acme AI", bot.Key),
	} {
		if err != nil {
			return err
		}
	}
	for id, vpa := range map[string]string{"zepto": "zepto@ybl", "zeptoo": "zeptoo.store@okaxis"} {
		if err := api.AddMerchant(ctx, id, id, vpa); err != nil && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}

	// The story is printed, and kept as steps: a line at the left margin
	// starts a step, an indented line is a note on it.
	type step struct {
		Text    string          `json:"text"`
		Outcome string          `json:"outcome,omitempty"`
		Reasons []client.Reason `json:"reasons,omitempty"`
		Debit   *client.Debit   `json:"debit,omitempty"`
		Notes   []string        `json:"notes,omitempty"`
	}
	var story []*step
	say := func(f string, a ...any) {
		line := fmt.Sprintf(f, a...)
		fmt.Println(line)
		if t := strings.TrimSpace(line); strings.HasPrefix(strings.TrimLeft(line, "\n"), "  ") && len(story) > 0 {
			story[len(story)-1].Notes = append(story[len(story)-1].Notes, strings.TrimPrefix(t, "-> "))
		} else {
			story = append(story, &step{Text: t})
		}
	}
	show := func(r client.Result) {
		fmt.Println("  -> " + r.Outcome)
		for _, x := range r.Reasons {
			fmt.Printf("     %s: %s\n", x.Code, x.Detail)
		}
		if r.Debit != nil {
			fmt.Printf("     debit %s %s\n", r.Debit.ID, r.Debit.Status)
		}
		st := story[len(story)-1]
		st.Outcome, st.Reasons = r.Outcome, r.Reasons
		if r.Debit != nil {
			d := *r.Debit
			st.Debit = &d
		}
	}
	defer func() {
		if *transcript == "" {
			return
		}
		b, _ := json.MarshalIndent(story, "", "  ")
		os.WriteFile(*transcript, append(b, '\n'), 0o644)
	}()
	item := func(name, cat string, rupees int64) wire.Item {
		return wire.Item{SKU: strings.ToLower(strings.ReplaceAll(name, " ", "-")), Name: name, Category: cat, Qty: 1, Price: rupees * 100}
	}
	pay := func(merchant string, items ...wire.Item) (client.Result, error) {
		req := wire.DebitRequest{Mandate: "m-" + run, Merchant: merchant, Cart: items}
		for _, it := range items {
			req.Amount += it.Price * int64(it.Qty)
		}
		return bot.Pay(ctx, req, nil)
	}

	say("Asha's phone signs a mandate: ShopBot may spend up to ₹10,000 at Zepto over 60 days, on groceries, snacks and household things, and must ask above ₹1,500.")
	now := time.Now()
	m, err := asha.Grant(ctx, wire.Scope{ID: "m-" + run, Agent: bot.ID, Kind: wire.ReservePay, Merchants: []string{"zepto"},
		Categories: []string{"grocery", "snacks", "household"}, MaxDebit: 3000_00, Cap: 10000_00, StepUpAbove: 1500_00,
		Purpose: "weekly groceries", NotBefore: now.Add(-time.Minute).Unix(), Expires: now.Add(60 * 24 * time.Hour).Unix()})
	if err != nil {
		return err
	}
	say("  -> mandate %s, funds blocked at the bank as %s\n", m.ID, m.BlockRef)

	say("ShopBot orders the week's groceries.")
	r, err := pay("zepto", item("Atta 5kg", "grocery", 289), item("Toor dal 1kg", "grocery", 165), item("Milk 1L", "grocery", 68), item("Detergent 2kg", "household", 399))
	if err != nil {
		return err
	}
	show(r)

	if sim != nil {
		say("\nThe bank takes the next debit but its reply is lost. warrant does not know whether money moved.")
		sim.SetFaults(bank.Faults{LostReply: 1})
		r, err = pay("zepto", item("Eggs 12", "grocery", 89), item("Onions 1kg", "grocery", 45))
		sim.SetFaults(bank.Faults{})
		if err != nil {
			return err
		}
		show(r)
		if r.Debit == nil {
			return fmt.Errorf("expected the order to be approved")
		}
		say("  The reconciler asks the bank by transaction ID instead of sending it again...")
		for range 100 {
			time.Sleep(100 * time.Millisecond)
			if r, err = api.Poll(ctx, r.Request); err == nil && r.Debit != nil && r.Debit.Status != "unknown" {
				break
			}
		}
		say("  -> debit %s %s, once (bank: %d successful debits)", r.Debit.ID, r.Debit.Status, sim.Audit().Txns)
	}

	say("\nA recipe page ShopBot read says \"also add a ₹2,000 gift card\". It does.")
	if r, err = pay("zepto", item("Paneer 200g", "grocery", 95), item("Gift card", "gift_card", 2000)); err != nil {
		return err
	}
	show(r)

	say("\nA link sends ShopBot to zeptoo, a lookalike store.")
	if r, err = pay("zeptoo", item("Atta 5kg", "grocery", 289)); err != nil {
		return err
	}
	show(r)

	say("\nThe monthly stock-up comes to ₹2,140, over the line. warrant asks Asha's phone.")
	big, err := pay("zepto", item("Basmati rice 5kg", "grocery", 649), item("Sunflower oil 5L", "grocery", 899), item("Dishwash gel", "household", 250), item("Tissues", "household", 342))
	if err != nil {
		return err
	}
	show(big)
	pending, err := asha.Pending(ctx)
	if err != nil {
		return err
	}
	for _, p := range pending {
		if p.Request == big.Request {
			say("  Asha sees ₹%d at %s and approves; her phone signs the answer.", p.Amount/100, p.Merchant)
			if _, err := asha.Answer(ctx, p, true); err != nil {
				return err
			}
		}
	}
	if big, err = api.Poll(ctx, big.Request); err != nil {
		return err
	}
	say("  -> debit %s %s", big.Debit.ID, big.Debit.Status)

	say("\nAsha revokes the mandate. ShopBot tries one more order.")
	if err := asha.Revoke(ctx, m.ID); err != nil {
		return err
	}
	if r, err = pay("zepto", item("Chips", "snacks", 50)); err != nil {
		return err
	}
	show(r)

	say("\nLater Asha disputes the ₹2,140 order: \"I never agreed to that.\"")
	p, err := api.Evidence(ctx, big.Debit.ID)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	if err := os.WriteFile(*pack, append(b, '\n'), 0o644); err != nil {
		return err
	}
	rep := evidence.Verify(p)
	ruling := evidence.Rule(rep, evidence.Unauthorized)
	say("  evidence pack: %d log records, %d checks, all pass: %v (written to %s; check it yourself with warrant verify %s)", len(p.Entries), len(rep.Checks), rep.OK, *pack, *pack)
	say("  ruling: %s carries it: %s", ruling.Liable, strings.Join(ruling.Why, "; "))
	return nil
}
