package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Rakshit-gen/warrant/eval"
	"github.com/Rakshit-gen/warrant/policy"
)

// EvalResult is results/eval.json.
type EvalResult struct {
	When      time.Time  `json:"when"`
	Seed      uint64     `json:"seed"`
	PerFamily int        `json:"per_family"`
	Note      string     `json:"note"`
	Modes     []EvalMode `json:"modes"`
}

// EvalMode is one policy over the whole corpus.
type EvalMode struct {
	Name     string         `json:"name"`
	Families []eval.Tally   `json:"families"`
	Totals   map[string]any `json:"totals"`
	TookMs   float64        `json:"took_ms"`
}

func cmdEval(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	dbURL := fs.String("db", os.Getenv("WARRANT_DB"), "Postgres URL; runs in schemas warrant_eval_*, which are dropped first")
	n := fs.Int("n", 40, "scenarios per family")
	seed := fs.Uint64("seed", 1, "corpus seed")
	out := fs.String("out", "results/eval.json", "where to write results")
	fs.Parse(args)

	scenarios := eval.Corpus(*seed, *n)
	res := EvalResult{When: time.Now().UTC(), Seed: *seed, PerFamily: *n,
		Note: "Synthetic corpus: generated sessions, not real traffic. A user is assumed to approve step-ups they wanted and decline ones they did not."}
	for _, mode := range []struct {
		name string
		opt  policy.Options
	}{{"caps_only", policy.Options{CapsOnly: true}}, {"warrant", policy.Options{}}} {
		t0 := time.Now()
		// A fixed start date, so the same seed gives the same sessions.
		clock := eval.NewClock(time.Date(2026, 11, 2, 10, 0, 0, 0, time.FixedZone("IST", 19800)))
		h, err := start(ctx, *dbURL, "warrant_eval_"+mode.name, mode.opt, clock.Now)
		if err != nil {
			return err
		}
		outs, err := eval.Run(ctx, h.api, clock, scenarios, "")
		h.close()
		if err != nil {
			return fmt.Errorf("%s: %w", mode.name, err)
		}
		fams := eval.Summarize(scenarios, outs)
		res.Modes = append(res.Modes, EvalMode{Name: mode.name, Families: fams, Totals: totals(fams), TookMs: float64(time.Since(t0).Microseconds()) / 1000})
		fmt.Fprintf(os.Stderr, "%s: %d scenarios, %d payment attempts in %v\n", mode.name, len(scenarios), len(outs), time.Since(t0).Round(time.Millisecond))
	}
	printEval(res)
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	return os.WriteFile(*out, append(b, '\n'), 0o644)
}

func totals(fams []eval.Tally) map[string]any {
	t := map[string]map[string]int64{}
	for _, k := range []string{eval.Benign, eval.Attack, eval.Evasion} {
		t[k] = map[string]int64{}
	}
	for _, f := range fams {
		m := t[f.Kind]
		m["wanted"] += int64(f.Wanted)
		m["wanted_paid"] += int64(f.WantedPaid)
		m["wanted_asked"] += int64(f.WantedAsked)
		m["wanted_refused"] += int64(f.WantedRefused)
		m["unwanted"] += int64(f.Unwanted)
		m["unwanted_paid"] += int64(f.UnwantedPaid)
		m["unwanted_asked"] += int64(f.UnwantedAsked)
		m["unwanted_stopped"] += int64(f.UnwantedStopped)
		m["money_tried"] += f.MoneyTried
		m["money_lost"] += f.MoneyLost
	}
	out := map[string]any{}
	for k, v := range t {
		out[k] = v
	}
	return out
}

func printEval(r EvalResult) {
	fmt.Printf("%-22s %-8s", "family", "kind")
	for _, m := range r.Modes {
		fmt.Printf("  %-26s", m.Name)
	}
	fmt.Println()
	for i, f := range r.Modes[0].Families {
		fmt.Printf("%-22s %-8s", f.Family, f.Kind)
		for _, m := range r.Modes {
			t := m.Families[i]
			if t.Kind == eval.Benign {
				fmt.Printf("  %-26s", fmt.Sprintf("asked %d/%d refused %d", t.WantedAsked, t.Wanted, t.WantedRefused))
			} else {
				fmt.Printf("  %-26s", fmt.Sprintf("paid %d/%d lost ₹%d", t.UnwantedPaid, t.Unwanted, t.MoneyLost/100))
			}
		}
		fmt.Println()
	}
}
