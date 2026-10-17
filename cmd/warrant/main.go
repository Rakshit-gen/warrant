// Command warrant is the toolbox around warrantd: keys, offline evidence
// checks, and the evaluation and benchmark harnesses.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/Rakshit-gen/warrant/evidence"
	"github.com/Rakshit-gen/warrant/sign"
)

var version = "dev"

type command struct {
	name, help string
	run        func(ctx context.Context, args []string) error
}

var commands []command

func init() {
	commands = []command{
		{"keygen", "make an Ed25519 key file and print its public key", cmdKeygen},
		{"verify", "check an evidence pack offline and rule on a claim", cmdVerify},
		{"demo", "walk through a short story: orders, an injection, a step-up, a lost reply, a revoke, a dispute", cmdDemo},
		{"eval", "replay the scenario corpus under caps-only and full policy", cmdEval},
		{"bench", "measure decision latency and check the books under bank faults", cmdBench},
		{"version", "print the version", func(context.Context, []string) error { fmt.Println("warrant", version); return nil }},
	}
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help" {
		usage()
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	for _, c := range commands {
		if c.name == os.Args[1] {
			if err := c.run(ctx, os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "warrant:", err)
				os.Exit(1)
			}
			return
		}
	}
	fmt.Fprintf(os.Stderr, "warrant: unknown command %q\n\n", os.Args[1])
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: warrant <command> [flags]")
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "  %-8s %s\n", c.name, c.help)
	}
}

func cmdKeygen(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "agent.key", "where to write the key")
	fs.Parse(args)
	k := sign.NewKey()
	if err := sign.SaveKey(*out, k); err != nil {
		return err
	}
	fmt.Println(k.PubString())
	return nil
}

func cmdVerify(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	claim := fs.String("claim", evidence.Unauthorized, "unauthorized, not_delivered or wrong_item")
	asJSON := fs.Bool("json", false, "print the report and ruling as JSON")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: warrant verify [-claim c] [-json] pack.json")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("one pack file is needed")
	}
	b, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	var p evidence.Pack
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("%s is not an evidence pack: %w", fs.Arg(0), err)
	}
	rep := evidence.Verify(p)
	ruling := evidence.Rule(rep, *claim)
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"report": rep, "ruling": ruling})
	}
	for _, c := range rep.Checks {
		mark := "ok  "
		if !c.OK {
			mark = "FAIL"
		}
		fmt.Printf("%s %s", mark, c.Name)
		if c.Detail != "" {
			fmt.Printf(": %s", c.Detail)
		}
		fmt.Println()
	}
	fmt.Printf("\nclaim %s: %s carries it", ruling.Claim, ruling.Liable)
	if ruling.Refund {
		fmt.Print(", the user is refunded")
	}
	fmt.Printf("\n  %s\n", strings.Join(ruling.Why, "\n  "))
	if !rep.OK {
		os.Exit(3)
	}
	return nil
}
