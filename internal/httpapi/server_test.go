package httpapi

import "testing"

func TestMoneyToMinor(t *testing.T) {
	tests := map[string]int64{"0.01": 1, "12.34": 1234, "1000.00": 100000}
	for input, expected := range tests {
		value, err := moneyToMinor(input)
		if err != nil || value != expected {
			t.Fatalf("%s: got %d, %v", input, value, err)
		}
	}
	for _, input := range []string{"0", "01.00", "-1.00", "1.2", "0.00", "1.-1", "1.+1", "+1.00", "92233720368547758.08"} {
		if _, err := moneyToMinor(input); err == nil {
			t.Fatalf("%s must be rejected", input)
		}
	}
}
