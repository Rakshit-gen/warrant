// Package client is the Go SDK for warrant: an Agent signs and sends
// debit requests, a Device stands in for the user's UPI app (it holds the
// device key that signs mandates, intents, step-up answers and
// revocations), and Admin registers the parties.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Rakshit-gen/warrant/evidence"
	"github.com/Rakshit-gen/warrant/sign"
	"github.com/Rakshit-gen/warrant/wire"
)

// API is a connection to warrantd.
type API struct {
	Base  string
	Token string // admin token, for registration
	HTTP  *http.Client
	// Now stamps requests; nil means time.Now.
	Now func() time.Time
}

// New talks to warrantd at base.
func New(base string) *API {
	return &API{Base: base, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Error is a non-2xx answer.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return fmt.Sprintf("warrant: %d %s", e.Status, e.Msg) }

// Do sends in as JSON and decodes the answer into out. 2xx answers decode;
// 403 decodes too, since a denied debit is an answer, not a failure.
func (a *API) Do(ctx context.Context, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.Base+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusForbidden {
		var e struct{ Error string }
		json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = string(b)
		}
		return resp.StatusCode, &Error{resp.StatusCode, e.Error}
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

// Nonce is a fresh random nonce.
func Nonce() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Registration.

func (a *API) AddUser(ctx context.Context, id, name string, device sign.Key) error {
	_, err := a.Do(ctx, "POST", "/v1/users", map[string]string{"id": id, "name": name, "device_key": device.PubString()}, nil)
	return err
}

func (a *API) AddAgent(ctx context.Context, id, name, developer string, key sign.Key) error {
	_, err := a.Do(ctx, "POST", "/v1/agents", map[string]string{"id": id, "name": name, "developer": developer, "key": key.PubString()}, nil)
	return err
}

func (a *API) AddMerchant(ctx context.Context, id, name, vpa string) error {
	_, err := a.Do(ctx, "POST", "/v1/merchants", map[string]string{"id": id, "name": name, "vpa": vpa}, nil)
	return err
}

// SuspendAgent takes an agent off. compromisedSince, if not zero, is when
// its developer says the key leaked.
func (a *API) SuspendAgent(ctx context.Context, id, reason string, compromisedSince time.Time) error {
	body := map[string]any{"reason": reason}
	if !compromisedSince.IsZero() {
		body["compromised_since"] = compromisedSince.UnixMilli()
	}
	_, err := a.Do(ctx, "POST", "/v1/agents/"+id+"/suspend", body, nil)
	return err
}

