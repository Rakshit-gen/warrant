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
