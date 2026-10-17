// Command warrantd runs the warrant API.
//
//	warrantd -db postgres://... [-bank http://localhost:8091] [-key warrantd.key]
//
// With no -bank it runs an in-process bank simulator, which is what the
// console demo uses.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Rakshit-gen/warrant/bank"
	"github.com/Rakshit-gen/warrant/policy"
	"github.com/Rakshit-gen/warrant/server"
	"github.com/Rakshit-gen/warrant/sign"
)

var version = "dev"

func main() {
	var (
		addr     = flag.String("addr", ":8090", "listen address")
		dbURL    = flag.String("db", os.Getenv("WARRANT_DB"), "Postgres URL (or WARRANT_DB)")
		bankURL  = flag.String("bank", "", "issuer API base URL; empty runs a simulator in process")
		bankWait = flag.Duration("bank-timeout", 2*time.Second, "how long to wait for the issuer before treating a debit as unknown")
		keyPath  = flag.String("key", "warrantd.key", "server signing key; created if missing")
		token    = flag.String("admin-token", os.Getenv("WARRANT_ADMIN_TOKEN"), "token for registering users, agents and merchants (or WARRANT_ADMIN_TOKEN); empty leaves it open")
		kafka    = flag.String("kafka", "", "comma separated Kafka brokers to relay events to; empty keeps them in the outbox")
		capsOnly = flag.Bool("caps-only", false, "check only identity, mandate and amount limits, as a plain mandate does (for comparison)")
		showVer  = flag.Bool("version", false, "print the version")
	)
	flag.Parse()
	if *showVer {
		fmt.Println("warrantd", version)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log, *addr, *dbURL, *bankURL, *bankWait, *keyPath, *token, *kafka, *capsOnly); err != nil {
		log.Error("warrantd", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, addr, dbURL, bankURL string, bankWait time.Duration, keyPath, token, kafka string, capsOnly bool) error {
	if dbURL == "" {
		return errors.New("-db is required (scripts/devdb.sh start prints a local one)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	key, err := sign.LoadKey(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key = sign.NewKey()
		if err = sign.SaveKey(keyPath, key); err == nil {
			log.Info("created signing key", "path", keyPath)
		}
	}
	if err != nil {
		return err
	}

	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return err
	}
	defer db.Close()

	var rail bank.Rail
	if bankURL == "" {
		rail = bank.NewSim(bank.Faults{}, uint64(time.Now().UnixNano()))
		log.Info("using the in-process bank simulator")
	} else {
		rail = bank.NewClient(strings.TrimRight(bankURL, "/"), bankWait)
	}

	s, err := server.New(ctx, server.Config{
		DB: db, Rail: rail, Key: key, AdminToken: token, Log: log,
		Policy: policy.Options{CapsOnly: capsOnly},
	})
	if err != nil {
		return err
	}
	if kafka != "" {
		go func() {
			if err := s.RelayOutbox(ctx, strings.Split(kafka, ",")); err != nil && ctx.Err() == nil {
				log.Error("kafka relay stopped", "err", err)
			}
		}()
	}
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Info("warrantd listening", "addr", addr, "server_key", key.PubString(), "caps_only", capsOnly)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-done
	return nil
}
