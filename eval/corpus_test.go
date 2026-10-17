package eval

import (
	"reflect"
	"testing"
)

func TestCorpusIsReproducibleAndLabeled(t *testing.T) {
	a, b := Corpus(3, 4), Corpus(3, 4)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("the same seed gave different corpora")
	}
	if len(a) != 4*len(Families) {
		t.Fatalf("%d scenarios", len(a))
	}
	for _, sc := range a {
		bad := 0
		for _, st := range sc.Steps {
			if st.Malicious {
				bad++
			}
			if st.Act == Pay && st.Amount == 0 && total(st.Cart) == 0 {
				t.Fatalf("%s: empty payment", sc.ID)
			}
		}
		if (sc.Kind == Benign) != (bad == 0) {
			t.Fatalf("%s is %s with %d unwanted payments", sc.ID, sc.Kind, bad)
		}
		if err := sc.Scope.Validate(); err == nil {
			t.Fatalf("%s: scope validates before the runner fills it in", sc.ID)
		}
	}
}
