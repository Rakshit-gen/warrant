// Package sign signs and verifies the documents warrant passes around: a
// user's mandate, an agent's debit request, a user's intent receipt or
// step-up answer, the server's log checkpoints.
//
// A signed document is an Envelope: the payload exactly as it was signed,
// the Ed25519 signature over those bytes, and the signer's public key. The
// payload is kept as raw bytes rather than re-encoded, so there is no
// canonical JSON to agree on: whoever verifies hashes the same bytes the
// signer did.
package sign

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Envelope is a signed payload. The payload travels as base64 so that no
// JSON encoder or database on the way can reorder or reformat the bytes
// the signature covers.
type Envelope struct {
	Payload []byte `json:"payload"` // the signed JSON document, base64 in JSON
	Sig     string `json:"sig"`     // base64 Ed25519 signature over Payload
	Key     string `json:"key"`     // base64 public key of the signer
}

// Key is an Ed25519 key pair.
type Key struct {
	Pub  ed25519.PublicKey
	Priv ed25519.PrivateKey
}

// NewKey makes a random key pair.
func NewKey() Key {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return Key{pub, priv}
}

// KeyFromSeed rebuilds a key pair from its 32-byte seed, for keys kept in
// files and for reproducible test data.
func KeyFromSeed(seed []byte) (Key, error) {
	if len(seed) != ed25519.SeedSize {
		return Key{}, fmt.Errorf("sign: seed is %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return Key{priv.Public().(ed25519.PublicKey), priv}, nil
}

// PubString is the public key as it appears in envelopes and the registry.
func (k Key) PubString() string { return base64.StdEncoding.EncodeToString(k.Pub) }

// Seal marshals v and signs it.
func (k Key) Seal(v any) (Envelope, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return Envelope{}, err
	}
	return k.SealBytes(b), nil
}

// SealBytes signs payload as is.
func (k Key) SealBytes(payload []byte) Envelope {
	return Envelope{
		Payload: append([]byte(nil), payload...),
		Sig:     base64.StdEncoding.EncodeToString(ed25519.Sign(k.Priv, payload)),
		Key:     k.PubString(),
	}
}

var (
	ErrBadKey = errors.New("sign: malformed public key")
	ErrBadSig = errors.New("sign: signature does not verify")
)

// Verify checks the signature against the envelope's own key. Callers must
// still check that the key is the one they expect, which VerifyBy does.
func (e Envelope) Verify() error {
	pub, err := ParsePub(e.Key)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(e.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrBadSig
	}
	if !ed25519.Verify(pub, e.Payload, sig) {
		return ErrBadSig
	}
	return nil
}

// VerifyBy checks the signature and that it was made by pub.
func (e Envelope) VerifyBy(pub string) error {
	if e.Key != pub {
		return fmt.Errorf("%w: signed by a different key", ErrBadSig)
	}
	return e.Verify()
}

// Open verifies the envelope against pub and decodes the payload into v.
func (e Envelope) Open(pub string, v any) error {
	if err := e.VerifyBy(pub); err != nil {
		return err
	}
	return json.Unmarshal(e.Payload, v)
}

// Digest is the hex SHA-256 of the payload. Two envelopes with the same
// digest carry the same signed bytes.
func (e Envelope) Digest() string {
	h := sha256.Sum256(e.Payload)
	return hex.EncodeToString(h[:])
}

// ParsePub decodes a base64 public key.
func ParsePub(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, ErrBadKey
	}
	return ed25519.PublicKey(b), nil
}

// LoadKey reads a key file: the base64 seed on one line.
func LoadKey(path string) (Key, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Key{}, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return Key{}, fmt.Errorf("sign: %s: %w", path, err)
	}
	return KeyFromSeed(seed)
}

// SaveKey writes k's seed to path, readable only by the owner. It will not
// overwrite an existing file.
func SaveKey(path string, k Key) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, base64.StdEncoding.EncodeToString(k.Priv.Seed()))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
