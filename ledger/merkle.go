// Package ledger is warrant's evidence log: an append-only Merkle tree of
// every mandate, request, decision, step-up and debit, hashed the way
// Certificate Transparency hashes certificates (RFC 6962), with checkpoints
// the server signs.
//
// The point is that a dispute pack can carry, for each record it relies
// on, a proof that the record sits in the log under a signed root. Anyone
// with the server's public key can check that offline. The server cannot
// quietly rewrite a decision after the fact without the old checkpoint,
// which the other side of the dispute may hold, giving it away.
package ledger

import (
	"crypto/sha256"
	"errors"
	"math/bits"
)

// Hash is a SHA-256 digest.
type Hash = [32]byte

// LeafHash hashes a record for the log.
func LeafHash(data []byte) Hash {
	return sha256.Sum256(append([]byte{0}, data...))
}

func nodeHash(l, r Hash) Hash {
	b := make([]byte, 0, 65)
	b = append(b, 1)
	b = append(b, l[:]...)
	b = append(b, r[:]...)
	return sha256.Sum256(b)
}

// Root is the tree head over leaves.
func Root(leaves []Hash) Hash {
	if len(leaves) == 0 {
		return sha256.Sum256(nil)
	}
	return subRoot(leaves)
}

func subRoot(leaves []Hash) Hash {
	if len(leaves) == 1 {
		return leaves[0]
	}
	k := split(len(leaves))
	return nodeHash(subRoot(leaves[:k]), subRoot(leaves[k:]))
}

// split is the largest power of two smaller than n.
func split(n int) int { return 1 << (bits.Len(uint(n-1)) - 1) }

// Proof is the audit path from leaf i to the root of the first n leaves.
func Proof(leaves []Hash, i int) []Hash {
	if i < 0 || i >= len(leaves) {
		return nil
	}
	return path(leaves, i)
}

func path(leaves []Hash, i int) []Hash {
	if len(leaves) <= 1 {
		return []Hash{}
	}
	k := split(len(leaves))
	if i < k {
		return append(path(leaves[:k], i), subRoot(leaves[k:]))
	}
	return append(path(leaves[k:], i-k), subRoot(leaves[:k]))
}

// ErrProof means an inclusion proof does not lead to the root.
var ErrProof = errors.New("ledger: inclusion proof does not match the root")

// VerifyInclusion checks that leaf is entry i of a tree of size n with the
// given root, using the RFC 9162 verification algorithm.
func VerifyInclusion(leaf Hash, i, n int, proof []Hash, root Hash) error {
	if i < 0 || i >= n {
		return ErrProof
	}
	fn, sn := i, n-1
	r := leaf
	for _, p := range proof {
		if sn == 0 {
			return ErrProof
		}
		if fn&1 == 1 || fn == sn {
			r = nodeHash(p, r)
			for fn&1 == 0 && fn != 0 {
				fn >>= 1
				sn >>= 1
			}
		} else {
			r = nodeHash(r, p)
		}
		fn >>= 1
		sn >>= 1
	}
	if sn != 0 || r != root {
		return ErrProof
	}
	return nil
}
