package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Rakshit-gen/warrant/bank"
	"github.com/Rakshit-gen/warrant/client"
	"github.com/Rakshit-gen/warrant/policy"
	"github.com/Rakshit-gen/warrant/server"
	"github.com/Rakshit-gen/warrant/sign"
)

// harness is a warrantd running in this process, with a simulated bank,
// in its own Postgres schema so it never touches other data.
type harness struct {
	api   *client.API
	sim   *bank.Sim
	srv   *server.Server
	db    *pgxpool.Pool
	close func()
}

func start(ctx context.Context, dbURL, schema string, opt policy.Options, now func() time.Time) (*harness, error) {
	if dbURL == "" {
		return nil, fmt.Errorf("-db is required (scripts/devdb.sh start prints a local one)")
	}
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	_, err = conn.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %[1]s CASCADE; CREATE SCHEMA %[1]s`, pgx.Identifier{schema}.Sanitize()))
	conn.Close(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 32
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	sim := bank.NewSim(bank.Faults{}, 1)
	if now != nil {
		sim.SetClock(now)
	}
	quiet := slog.New(slog.DiscardHandler)
	s, err := server.New(ctx, server.Config{DB: db, Rail: sim, Key: sign.NewKey(), Policy: opt, Now: now, Log: quiet})
	if err != nil {
		db.Close()
		return nil, err
	}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.Run(rctx); close(done) }()
	hs := httptest.NewServer(s.Handler())
	api := client.New(hs.URL)
	api.Now = now
	return &harness{api: api, sim: sim, srv: s, db: db, close: func() { cancel(); <-done; hs.Close(); db.Close() }}, nil
}
