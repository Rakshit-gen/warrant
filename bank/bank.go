// Package bank is the rail under warrant: the issuer bank that holds a
// user's Reserve Pay block or Circle delegation and executes debits on it.
//
// Sim is an in-memory issuer that follows the rules NPCI sets for Reserve
// Pay (one block per user and merchant, debits only up to the block, the
// block released on revoke or expiry) and that can misbehave the way real
// rails do: slow replies, declines, timeouts where nothing happened, and
// the nasty one, a debit that went through but whose reply never arrived.
// The caller can't tell the last two apart and has to ask Status.
package bank

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

// Debit outcomes.
const (
	Success  = "success"
	Declined = "declined"
	Unknown  = "unknown" // no such transaction at the bank
)

// Errors a caller must tell apart.
var (
	// ErrTimeout means no answer: the debit may or may not have happened.
	ErrTimeout = errors.New("bank: timed out")
	// ErrDeclined means the bank refused it and nothing moved.
	ErrDeclined = errors.New("bank: declined")
)

// Rail is what warrant needs from a bank.
type Rail interface {
	Block(ctx context.Context, b BlockReq) (string, error)
	Debit(ctx context.Context, block, txn string, amount int64) error
	Status(ctx context.Context, txn string) (string, error)
	Revoke(ctx context.Context, block string) error
}

// BlockReq asks the issuer to block funds (Reserve Pay) or record a
// delegation (Circle) for a user and merchant.
type BlockReq struct {
	User     string    `json:"user"`
	Merchant string    `json:"merchant"` // for circle, empty
	Kind     string    `json:"kind"`
	Amount   int64     `json:"amount"`
	Expires  time.Time `json:"expires"`
}

// Faults are probabilities per debit call, plus latency.
type Faults struct {
	Latency   time.Duration `json:"latency"`
	Jitter    time.Duration `json:"jitter"`
	Decline   float64       `json:"decline"`    // refused, nothing moved
	Drop      float64       `json:"drop"`       // timed out, nothing moved
	LostReply float64       `json:"lost_reply"` // moved, but the caller times out
}

type block struct {
	BlockReq
	used    int64
	revoked bool
}

type txn struct {
	block  string
	amount int64
	status string
}

// Sim is an in-memory issuer bank.
type Sim struct {
	mu     sync.Mutex
	f      Faults
	rng    *rand.Rand
	seq    int
	blocks map[string]*block
	active map[string]string // user/merchant -> block, for one block per pair
	txns   map[string]*txn
	now    func() time.Time
}

// NewSim makes a bank with the given faults and random seed.
func NewSim(f Faults, seed uint64) *Sim {
	return &Sim{f: f, rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), blocks: map[string]*block{},
		active: map[string]string{}, txns: map[string]*txn{}, now: time.Now}
}

// SetClock makes the bank read time from now, for replays on a moved clock.
func (s *Sim) SetClock(now func() time.Time) {
	s.mu.Lock()
	s.now = now
	s.mu.Unlock()
}

// SetFaults changes the faults from now on.
func (s *Sim) SetFaults(f Faults) {
	s.mu.Lock()
	s.f = f
	s.mu.Unlock()
}

func (s *Sim) Block(ctx context.Context, b BlockReq) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pair := b.User + "/" + b.Merchant
	if id, ok := s.active[pair]; ok {
		old := s.blocks[id]
		if !old.revoked && s.now().Before(old.Expires) {
			return "", fmt.Errorf("%w: user already has an active block with %s", ErrDeclined, b.Merchant)
		}
	}
	s.seq++
	id := fmt.Sprintf("blk_%06d", s.seq)
	s.blocks[id] = &block{BlockReq: b}
	s.active[pair] = id
	return id, nil
}

func (s *Sim) Debit(ctx context.Context, blockID, txnID string, amount int64) error {
	s.mu.Lock()
	f := s.f
	roll := s.rng.Float64()
	delay := f.Latency
	if f.Jitter > 0 {
		delay += time.Duration(s.rng.Int64N(int64(f.Jitter)))
	}
	s.mu.Unlock()
	if err := sleep(ctx, delay); err != nil {
		return ErrTimeout
	}

	switch {
	case roll < f.Drop:
		return ErrTimeout
	case roll < f.Drop+f.Decline:
		return ErrDeclined
	}
	err := s.apply(blockID, txnID, amount)
	if err == nil && roll < f.Drop+f.Decline+f.LostReply {
		return ErrTimeout
	}
	return err
}

// apply moves the money, once per transaction ID.
func (s *Sim) apply(blockID, txnID string, amount int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.txns[txnID]; ok {
		if t.status == Success {
			return nil // a retry of a debit that went through
		}
		return ErrDeclined
	}
	b, ok := s.blocks[blockID]
	status := Success
	var err error
	switch {
	case !ok:
		err = fmt.Errorf("%w: no block %s", ErrDeclined, blockID)
	case b.revoked:
		err = fmt.Errorf("%w: block %s is revoked", ErrDeclined, blockID)
	case !s.now().Before(b.Expires):
		err = fmt.Errorf("%w: block %s expired", ErrDeclined, blockID)
	case b.used+amount > b.Amount:
		err = fmt.Errorf("%w: block %s has %d left", ErrDeclined, blockID, b.Amount-b.used)
	}
	if err != nil {
		status = Declined
	} else {
		b.used += amount
	}
	s.txns[txnID] = &txn{block: blockID, amount: amount, status: status}
	return err
}

func (s *Sim) Status(ctx context.Context, txnID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.txns[txnID]; ok {
		return t.status, nil
	}
	return Unknown, nil
}

func (s *Sim) Revoke(ctx context.Context, blockID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blocks[blockID]
	if !ok {
		return fmt.Errorf("%w: no block %s", ErrDeclined, blockID)
	}
	b.revoked = true
	return nil
}

