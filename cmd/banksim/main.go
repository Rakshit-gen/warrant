// Command banksim serves the simulated issuer over HTTP, so warrantd can be
// run against a bank in another process that drops and loses replies.
//
//	banksim -addr :8091 -lost-reply 0.05 -latency 40ms
//
// Faults can be changed while it runs with PUT /faults.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/Rakshit-gen/warrant/bank"
)

func main() {
	var f bank.Faults
	addr := flag.String("addr", ":8091", "listen address")
	seed := flag.Uint64("seed", 1, "random seed for faults")
	flag.DurationVar(&f.Latency, "latency", 0, "base latency per debit")
	flag.DurationVar(&f.Jitter, "jitter", 0, "extra random latency, up to this much")
	flag.Float64Var(&f.Decline, "decline", 0, "share of debits declined")
	flag.Float64Var(&f.Drop, "drop", 0, "share of debits that time out without moving money")
	flag.Float64Var(&f.LostReply, "lost-reply", 0, "share of debits that move money but never answer")
	flag.Parse()
	log.Printf("banksim on %s, faults %+v", *addr, f)
	srv := &http.Server{Addr: *addr, Handler: bank.Handler(bank.NewSim(f, *seed)), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
