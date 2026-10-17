package bank

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLostReplyStillMovesMoneyOnce(t *testing.T) {
	ctx := context.Background()
	s := NewSim(Faults{LostReply: 1}, 1)
	srv := httptest.NewServer(Handler(s))
	defer srv.Close()
	c := NewClient(srv.URL, time.Second)

	blk, err := c.Block(ctx, BlockReq{User: "u", Merchant: "m", Kind: "reserve_pay", Amount: 1000, Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Debit(ctx, blk, "t1", 400); !errors.Is(err, ErrTimeout) {
		t.Fatalf("want a timeout, got %v", err)
	}
	if st, _ := c.Status(ctx, "t1"); st != Success {
		t.Fatalf("status %q, the debit went through", st)
	}
	// Retrying the same transaction must not move money again.
	s.SetFaults(Faults{})
	if err := c.Debit(ctx, blk, "t1", 400); err != nil {
		t.Fatal(err)
	}
	if a := s.Audit(); a.Debited[blk] != 400 || a.Txns != 1 {
		t.Fatalf("audit %+v", a)
	}
}

func TestBlockRules(t *testing.T) {
	ctx := context.Background()
	s := NewSim(Faults{}, 1)
	exp := time.Now().Add(time.Hour)
	blk, _ := s.Block(ctx, BlockReq{User: "u", Merchant: "m", Amount: 1000, Expires: exp})
	if _, err := s.Block(ctx, BlockReq{User: "u", Merchant: "m", Amount: 1000, Expires: exp}); !errors.Is(err, ErrDeclined) {
		t.Fatal("second active block for the same merchant was allowed")
	}
	if err := s.Debit(ctx, blk, "a", 700); err != nil {
		t.Fatal(err)
	}
	if err := s.Debit(ctx, blk, "b", 400); !errors.Is(err, ErrDeclined) {
		t.Fatal("debit past the block was allowed")
	}
	s.Revoke(ctx, blk)
	if err := s.Debit(ctx, blk, "c", 100); !errors.Is(err, ErrDeclined) {
		t.Fatal("debit on a revoked block was allowed")
	}
}
