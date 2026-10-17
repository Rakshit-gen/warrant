package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Rakshit-gen/warrant/bank"
	"github.com/Rakshit-gen/warrant/client"
	"github.com/Rakshit-gen/warrant/policy"
	"github.com/Rakshit-gen/warrant/sign"
	"github.com/Rakshit-gen/warrant/wire"
)

// BenchResult is results/bench.json.
type BenchResult struct {
	When     time.Time   `json:"when"`
	Machine  string      `json:"machine"`
	Postgres string      `json:"postgres"`
	Load     LoadResult  `json:"load"`
	Books    BooksResult `json:"books"`
}

// LoadResult is decision latency with many agents paying at once.
type LoadResult struct {
	Mandates    int                `json:"mandates"`
	Workers     int                `json:"workers"`
	Requests    int                `json:"requests"`
	Seconds     float64            `json:"seconds"`
	PerSecond   float64            `json:"per_second"`
	ByOutcome   map[string]int     `json:"by_outcome"`
	LatencyMs   map[string]float64 `json:"latency_ms"`         // all requests: p50, p90, p99, max
	ApproveMs   map[string]float64 `json:"approve_latency_ms"` // approvals, bank call included
	BankLatency string             `json:"bank_latency"`
}

// BooksResult is the exactly-once check: many approvals racing on a few
// mandates through a bank that drops and loses replies.
type BooksResult struct {
	Mandates        int     `json:"mandates"`
	Requests        int     `json:"requests"`
	Faults          string  `json:"faults"`
	Approved        int     `json:"approved"`
	ApprovedWant    int     `json:"approved_want"` // what the caps allow exactly
	LeftUnknown     int     `json:"left_unknown"`  // debits the reply never came back for
	Resent          int     `json:"resent"`        // debits the reconciler sent again after the bank had no record
	BankDebits      int     `json:"bank_debits"`
	WarrantDebits   int     `json:"warrant_debits"`
	MismatchBlocks  int     `json:"mismatched_blocks"` // bank total != warrant total
	OverspentBlocks int     `json:"overspent_blocks"`
	SettleSeconds   float64 `json:"settle_seconds"`
}

func cmdBench(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	dbURL := fs.String("db", os.Getenv("WARRANT_DB"), "Postgres URL; runs in schemas warrant_bench_*, which are dropped first")
	mandates := fs.Int("mandates", 500, "mandates in the load run")
	workers := fs.Int("workers", 32, "concurrent agents")
	requests := fs.Int("requests", 10000, "requests in the load run")
	bankLat := fs.Duration("bank-latency", 30*time.Millisecond, "simulated issuer latency per debit in the load run")
	out := fs.String("out", "results/bench.json", "where to write results")
	fs.Parse(args)

	res := BenchResult{When: time.Now().UTC(), Machine: fmt.Sprintf("%s/%s, %d CPUs", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())}
	var err error
	if res.Load, err = benchLoad(ctx, *dbURL, *mandates, *workers, *requests, *bankLat, &res.Postgres); err != nil {
		return err
	}
	fmt.Printf("load: %d requests, %d workers, %.0f/s; p50 %.1fms p99 %.1fms; approvals p50 %.1fms p99 %.1fms; %v\n",
		res.Load.Requests, res.Load.Workers, res.Load.PerSecond, res.Load.LatencyMs["p50"], res.Load.LatencyMs["p99"],
		res.Load.ApproveMs["p50"], res.Load.ApproveMs["p99"], res.Load.ByOutcome)
	if res.Books, err = benchBooks(ctx, *dbURL); err != nil {
		return err
	}
	b := res.Books
	fmt.Printf("books: %d racing requests on %d mandates, %s: approved %d (caps allow %d), %d replies lost, %d resent, bank %d debits / warrant %d, %d mismatched, %d overspent\n",
		b.Requests, b.Mandates, b.Faults, b.Approved, b.ApprovedWant, b.LeftUnknown, b.Resent, b.BankDebits, b.WarrantDebits, b.MismatchBlocks, b.OverspentBlocks)
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	j, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(*out, append(j, '\n'), 0o644); err != nil {
		return err
	}
	if b.MismatchBlocks > 0 || b.OverspentBlocks > 0 || b.Approved != b.ApprovedWant || b.BankDebits != b.WarrantDebits {
		return fmt.Errorf("the books do not balance")
	}
	return nil
}

type party struct {
	dev     client.Device
	agent   client.Agent
	mandate string
}

// enroll registers n users, agents and mandates on merchant zepto.
func enroll(ctx context.Context, api *client.API, n int, sc wire.Scope) ([]party, error) {
	if err := api.AddMerchant(ctx, "zepto", "Zepto", "zepto@ybl"); err != nil {
		return nil, err
	}
	ps := make([]party, n)
	for i := range ps {
		p := party{dev: client.Device{API: api, User: fmt.Sprintf("u%d", i), Key: sign.NewKey()},
			agent: client.Agent{API: api, ID: fmt.Sprintf("a%d", i), Key: sign.NewKey()}, mandate: fmt.Sprintf("m%d", i)}
		if err := api.AddUser(ctx, p.dev.User, p.dev.User, p.dev.Key); err != nil {
			return nil, err
		}
		if err := api.AddAgent(ctx, p.agent.ID, p.agent.ID, "bench", p.agent.Key); err != nil {
			return nil, err
		}
		s := sc
		s.ID, s.Agent = p.mandate, p.agent.ID
		if _, err := p.dev.Grant(ctx, s); err != nil {
			return nil, err
		}
		ps[i] = p
	}
	return ps, nil
}

func benchLoad(ctx context.Context, dbURL string, mandates, workers, requests int, bankLat time.Duration, pgVersion *string) (LoadResult, error) {
	h, err := start(ctx, dbURL, "warrant_bench_load", policy.Options{}, nil)
	if err != nil {
		return LoadResult{}, err
	}
	defer h.close()
	h.db.QueryRow(ctx, `SHOW server_version`).Scan(pgVersion)
	h.api.HTTP.Transport = &http.Transport{MaxIdleConnsPerHost: workers}
	now := time.Now()
	ps, err := enroll(ctx, h.api, mandates, wire.Scope{Kind: wire.ReservePay, Merchants: []string{"zepto"}, Categories: []string{"grocery"},
		MaxDebit: 1000_00, Cap: 10000_00, StepUpAbove: 800_00, NotBefore: now.Add(-time.Minute).Unix(), Expires: now.Add(24 * time.Hour).Unix()})
	if err != nil {
		return LoadResult{}, err
	}
	h.sim.SetFaults(bank.Faults{Latency: bankLat})

	var next atomic.Int64
	var mu sync.Mutex
	all := make([]float64, 0, requests)
	approve := []float64{}
	by := map[string]int{}
	var wg sync.WaitGroup
	t0 := time.Now()
	var firstErr error
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= requests {
					return
				}
				p := ps[i%len(ps)]
				amt := int64(20_00 + (i*37)%60*1_00)
				req := wire.DebitRequest{Mandate: p.mandate, Merchant: "zepto", Amount: amt,
					Cart: []wire.Item{{SKU: "milk", Name: "Milk 1L", Category: "grocery", Qty: 1, Price: amt}}}
				s := time.Now()
				r, err := p.agent.Pay(ctx, req, nil)
				ms := float64(time.Since(s).Microseconds()) / 1000
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					by["error"]++
				} else {
					by[r.Outcome]++
					all = append(all, ms)
					if r.Outcome == policy.Approve {
						approve = append(approve, ms)
					}
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	took := time.Since(t0).Seconds()
	if firstErr != nil {
		return LoadResult{}, fmt.Errorf("load: %w", firstErr)
	}
	h.srv.Wait()
	return LoadResult{Mandates: mandates, Workers: workers, Requests: requests, Seconds: took, PerSecond: float64(requests) / took,
		ByOutcome: by, LatencyMs: pcts(all), ApproveMs: pcts(approve), BankLatency: bankLat.String()}, nil
}

func pcts(xs []float64) map[string]float64 {
	if len(xs) == 0 {
		return map[string]float64{}
	}
	slices.Sort(xs)
	at := func(q float64) float64 { return xs[min(len(xs)-1, int(q*float64(len(xs))))] }
	return map[string]float64{"p50": at(0.5), "p90": at(0.9), "p99": at(0.99), "max": xs[len(xs)-1]}
}

func benchBooks(ctx context.Context, dbURL string) (BooksResult, error) {
	// Caps only: this run is about money, and the anomaly checks would
	// turn most of a burst into step-ups before the cap is reached.
	h, err := start(ctx, dbURL, "warrant_bench_books", policy.Options{CapsOnly: true}, nil)
	if err != nil {
		return BooksResult{}, err
	}
	defer h.close()
	const mandates, perMandate, amount, capPaise = 10, 50, 300_00, 5000_00
	now := time.Now()
	ps, err := enroll(ctx, h.api, mandates, wire.Scope{Kind: wire.ReservePay, Merchants: []string{"zepto"}, MaxDebit: 1000_00, Cap: capPaise,
		NotBefore: now.Add(-time.Minute).Unix(), Expires: now.Add(24 * time.Hour).Unix()})
	if err != nil {
		return BooksResult{}, err
	}
	f := bank.Faults{Jitter: 20 * time.Millisecond, Drop: 0.05, LostReply: 0.15}
	h.sim.SetFaults(f)
	res := BooksResult{Mandates: mandates, Requests: mandates * perMandate, ApprovedWant: mandates * int(capPaise/amount),
		Faults: fmt.Sprintf("%.0f%% dropped, %.0f%% lost replies, up to %v jitter", f.Drop*100, f.LostReply*100, f.Jitter)}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i := range res.Requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := ps[i%mandates]
			r, err := p.agent.Pay(ctx, wire.DebitRequest{Mandate: p.mandate, Merchant: "zepto", Amount: amount,
				Cart: []wire.Item{{SKU: "atta", Name: "Atta 5kg", Category: "grocery", Qty: 1, Price: amount}}}, nil)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				firstErr = err
				return
			}
			if r.Outcome == policy.Approve {
				res.Approved++
				if r.Debit != nil && r.Debit.Status == "unknown" {
					res.LeftUnknown++
				}
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return res, firstErr
	}
	// From here the bank behaves; the reconciler settles what it can.
	h.sim.SetFaults(bank.Faults{})
	t0 := time.Now()
	for {
		var open int
		if err := h.db.QueryRow(ctx, `SELECT count(*) FROM debits WHERE status IN ('pending', 'unknown')`).Scan(&open); err != nil {
			return res, err
		}
		if open == 0 {
			break
		}
		if time.Since(t0) > time.Minute {
			return res, fmt.Errorf("%d debits still open after a minute", open)
		}
		time.Sleep(100 * time.Millisecond)
	}
	res.SettleSeconds = time.Since(t0).Seconds()
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM debits WHERE attempts > 1`).Scan(&res.Resent); err != nil {
		return res, err
	}

	audit := h.sim.Audit()
	res.BankDebits = audit.Txns
	rows, err := h.db.Query(ctx, `SELECT m.block_ref, coalesce(sum(d.amount) FILTER (WHERE d.status = 'succeeded'), 0), count(d.id) FILTER (WHERE d.status = 'succeeded')
		FROM mandates m LEFT JOIN debits d ON d.mandate_id = m.id GROUP BY m.block_ref`)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var block string
		var sum int64
		var n int
		if err := rows.Scan(&block, &sum, &n); err != nil {
			return res, err
		}
		res.WarrantDebits += n
		if audit.Debited[block] != sum {
			res.MismatchBlocks++
		}
		if audit.Debited[block] > capPaise {
			res.OverspentBlocks++
		}
	}
	return res, rows.Err()
}
