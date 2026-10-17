package ledger

import (
	"fmt"
	"testing"
)

func TestEveryLeafProvesAtEverySize(t *testing.T) {
	var leaves []Hash
	for n := 1; n <= 70; n++ {
		leaves = append(leaves, LeafHash([]byte(fmt.Sprint("record ", n))))
		root := Root(leaves)
		for i := range leaves {
			if err := VerifyInclusion(leaves[i], i, n, Proof(leaves, i), root); err != nil {
				t.Fatalf("n=%d i=%d: %v", n, i, err)
			}
		}
	}
}

func TestTamperedLeafFails(t *testing.T) {
	var leaves []Hash
	for i := range 13 {
		leaves = append(leaves, LeafHash([]byte{byte(i)}))
	}
	root := Root(leaves)
	p := Proof(leaves, 5)
	if VerifyInclusion(LeafHash([]byte("forged")), 5, 13, p, root) == nil {
		t.Fatal("a different record verified")
	}
	if VerifyInclusion(leaves[5], 6, 13, p, root) == nil {
		t.Fatal("the right record at the wrong index verified")
	}
	if VerifyInclusion(leaves[5], 5, 13, p, Root(leaves[:12])) == nil {
		t.Fatal("the proof verified against another tree's root")
	}
}
