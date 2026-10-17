package wire

import "testing"

func TestRupees(t *testing.T) {
	for in, want := range map[int64]string{0: "₹0", 5_00: "₹5", 2095_50: "₹2,095.50", 10000_00: "₹10,000", 100000_00: "₹1,00,000", 12345678_00: "₹1,23,45,678", -150_00: "-₹150"} {
		if got := Rupees(in); got != want {
			t.Errorf("Rupees(%d) = %s, want %s", in, got, want)
		}
	}
}
