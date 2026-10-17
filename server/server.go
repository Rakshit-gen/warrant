// Package server is warrantd: the HTTP API agents, user apps and the
// console talk to, the debit executor, the reconciler, and the sequencer
// that keeps the evidence log.
package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Rakshit-gen/warrant/bank"
	"github.com/Rakshit-gen/warrant/policy"
	"github.com/Rakshit-gen/warrant/sign"
)

//go:embed schema.sql
var schema string

// Config is how a server is put together.
type Config struct {
	DB     *pgxpool.Pool
	Rail   bank.Rail
	Key    sign.Key // signs log checkpoints
	Policy policy.Options
	// AdminToken guards registration of users, agents and merchants. Empty
	// leaves registration open, which only suits a local demo.
	AdminToken string
	// Now is the clock; nil means time.Now. The evaluation harness moves it.
	Now func() time.Time
	// StepUpTTL is how long a user has to answer; UPI Circle gives ten
	// minutes for a partially delegated payment.
	StepUpTTL time.Duration
	Log       *slog.Logger
}

// Server is a running warrantd.
type Server struct {
	cfg    Config
	db     *pgxpool.Pool
	now    func() time.Time
	log    *slog.Logger
	events *broker
	tree   *tree
	m      *metrics
	// executing tracks debits handed to the bank, so tests and shutdown can
	// wait for them.
	executing sync.WaitGroup
}

// New applies the schema and loads the log.
func New(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.StepUpTTL == 0 {
		cfg.StepUpTTL = 10 * time.Minute
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if _, err := cfg.DB.Exec(ctx, schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	s := &Server{cfg: cfg, db: cfg.DB, now: cfg.Now, log: cfg.Log, events: newBroker(), m: newMetrics()}
	t, err := loadTree(ctx, cfg.DB)
	if err != nil {
		return nil, err
	}
	s.tree = t
	return s, nil
}

// Run starts the background loops: sequencing the log, reconciling debits
// the bank never answered, expiring step-ups. It returns when ctx ends.
func (s *Server) Run(ctx context.Context) {
	var wg sync.WaitGroup
	loop := func(every time.Duration, f func(context.Context) error, name string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				if err := f(ctx); err != nil && ctx.Err() == nil {
					s.log.Error(name, "err", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}
	loop(200*time.Millisecond, func(ctx context.Context) error { _, err := s.Sequence(ctx); return err }, "sequence")
	loop(time.Second, func(ctx context.Context) error { _, err := s.Reconcile(ctx, 2*time.Second); return err }, "reconcile")
	loop(5*time.Second, s.expireStepUps, "expire step-ups")
	wg.Wait()
	s.executing.Wait()
}

// Wait blocks until every debit handed to the bank has an answer or has
// been marked unknown.
func (s *Server) Wait() { s.executing.Wait() }

// Handler is the HTTP API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	admin := s.admin
	mux.HandleFunc("POST /v1/users", admin(s.handleUser))
	mux.HandleFunc("POST /v1/agents", admin(s.handleAgent))
	mux.HandleFunc("POST /v1/agents/{id}/suspend", admin(s.handleSuspend))
	mux.HandleFunc("POST /v1/merchants", admin(s.handleMerchant))
	mux.HandleFunc("GET /v1/merchants", s.handleMerchants)
	mux.HandleFunc("GET /v1/agents", s.handleAgents)

	mux.HandleFunc("POST /v1/mandates", s.handleMandate)
	mux.HandleFunc("GET /v1/mandates/{id}", s.handleGetMandate)
	mux.HandleFunc("POST /v1/mandates/{id}/revoke", s.handleRevoke)
	mux.HandleFunc("GET /v1/users/{id}/mandates", s.handleUserMandates)
	mux.HandleFunc("GET /v1/users/{id}/stepups", s.handleUserStepUps)
	mux.HandleFunc("POST /v1/stepups/{id}", s.handleStepUp)

	mux.HandleFunc("POST /v1/debits", s.handleDebit)
	mux.HandleFunc("GET /v1/requests/{id}", s.handleGetRequest)
	mux.HandleFunc("GET /v1/mandates/{id}/requests", s.handleMandateRequests)

	mux.HandleFunc("GET /v1/debits/{id}/evidence", s.handleEvidence)
	mux.HandleFunc("POST /v1/disputes", s.handleDispute)
	mux.HandleFunc("GET /v1/log/checkpoint", s.handleCheckpoint)

	mux.HandleFunc("GET /v1/events", s.handleEvents)
	mux.HandleFunc("GET /metrics", s.m.handle)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.db.Ping(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok\n"))
	})
	return s.cors(s.m.wrap(mux))
}

func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AdminToken != "" && r.Header.Get("Authorization") != "Bearer "+s.cfg.AdminToken {
			writeErr(w, http.StatusUnauthorized, "admin token required")
			return
		}
		h(w, r)
	}
}

// cors lets the console on localhost call the API from the browser.
func (s *Server) cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); strings.HasPrefix(o, "http://localhost:") || strings.HasPrefix(o, "http://127.0.0.1:") {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return false
	}
	return true
}

// tx runs f in a transaction.
func (s *Server) tx(ctx context.Context, f func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.db, f)
}

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func notFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
