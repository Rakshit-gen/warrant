package bank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Handler serves a Sim over HTTP, so warrantd can talk to a bank in another
// process the way it would talk to a real issuer's API.
func Handler(s *Sim) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /blocks", func(w http.ResponseWriter, r *http.Request) {
		var b BlockReq
		if !decode(w, r, &b) {
			return
		}
		id, err := s.Block(r.Context(), b)
		reply(w, map[string]string{"block": id}, err)
	})
	mux.HandleFunc("POST /debits", func(w http.ResponseWriter, r *http.Request) {
		var d struct {
			Block  string `json:"block"`
			Txn    string `json:"txn"`
			Amount int64  `json:"amount"`
		}
		if !decode(w, r, &d) {
			return
		}
		err := s.Debit(r.Context(), d.Block, d.Txn, d.Amount)
		if errors.Is(err, ErrTimeout) {
			// Hang up without an answer, as a timed-out issuer does.
			panic(http.ErrAbortHandler)
		}
		reply(w, map[string]string{"status": Success}, err)
	})
	mux.HandleFunc("GET /debits/{txn}", func(w http.ResponseWriter, r *http.Request) {
		st, err := s.Status(r.Context(), r.PathValue("txn"))
		reply(w, map[string]string{"status": st}, err)
	})
	mux.HandleFunc("POST /blocks/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]string{}, s.Revoke(r.Context(), r.PathValue("id")))
	})
	mux.HandleFunc("GET /audit", func(w http.ResponseWriter, r *http.Request) {
		reply(w, s.Audit(), nil)
	})
	mux.HandleFunc("PUT /faults", func(w http.ResponseWriter, r *http.Request) {
		var f Faults
		if !decode(w, r, &f) {
			return
		}
		s.SetFaults(f)
		reply(w, f, nil)
	})
	return mux
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrDeclined) {
			code = http.StatusUnprocessableEntity
		}
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(v)
}

// Client is a Rail over HTTP.
type Client struct {
	Base string
	HTTP *http.Client
}

// NewClient talks to a bank at base. Debits give up after timeout.
func NewClient(base string, timeout time.Duration) *Client {
	return &Client{Base: base, HTTP: &http.Client{Timeout: timeout}}
}

func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		json.NewEncoder(&body).Encode(in)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// No answer: the caller can't know what happened.
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnprocessableEntity {
		var e struct{ Error string }
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("%w: %s", ErrDeclined, e.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: bank answered %s", ErrTimeout, resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) Block(ctx context.Context, b BlockReq) (string, error) {
	var out struct{ Block string }
	err := c.call(ctx, "POST", "/blocks", b, &out)
	return out.Block, err
}

func (c *Client) Debit(ctx context.Context, block, txn string, amount int64) error {
	return c.call(ctx, "POST", "/debits", map[string]any{"block": block, "txn": txn, "amount": amount}, nil)
}

func (c *Client) Status(ctx context.Context, txn string) (string, error) {
	var out struct{ Status string }
	err := c.call(ctx, "GET", "/debits/"+txn, nil, &out)
	return out.Status, err
}

func (c *Client) Revoke(ctx context.Context, block string) error {
	return c.call(ctx, "POST", "/blocks/"+block+"/revoke", nil, nil)
}

// Audit fetches the bank's own totals.
func (c *Client) Audit(ctx context.Context) (Audit, error) {
	var a Audit
	err := c.call(ctx, "GET", "/audit", nil, &a)
	return a, err
}
