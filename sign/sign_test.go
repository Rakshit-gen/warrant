package sign

import (
	"encoding/json"
	"testing"
)

// A signed document must survive being re-encoded on the way, byte for
// byte, including whitespace and key order a re-encoder would change.
func TestEnvelopeKeepsSignedBytes(t *testing.T) {
	k := NewKey()
	payload := []byte("{\n  \"z\": 1,\n  \"a\": \"x\"\n}")
	env := k.SealBytes(payload)
	b, err := json.Marshal(map[string]any{"wrapped": env})
	if err != nil {
		t.Fatal(err)
	}
	var back struct{ Wrapped Envelope }
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if err := back.Wrapped.VerifyBy(k.PubString()); err != nil {
		t.Fatal(err)
	}
	if back.Wrapped.Digest() != env.Digest() {
		t.Fatal("digest changed")
	}
	back.Wrapped.Payload[3] ^= 1
	if back.Wrapped.Verify() == nil {
		t.Fatal("a changed payload still verifies")
	}
	if env.VerifyBy(NewKey().PubString()) == nil {
		t.Fatal("verified against the wrong key")
	}
}
