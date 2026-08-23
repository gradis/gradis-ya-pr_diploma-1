package money

import (
	"encoding/json"
	"math"
	"testing"
)

func TestParseCents(t *testing.T) {
	tests := []struct {
		value string
		want  int64
		valid bool
	}{
		{value: "0", want: 0, valid: true},
		{value: "42", want: 4200, valid: true},
		{value: "500.5", want: 50050, valid: true},
		{value: "1.01", want: 101, valid: true},
		{value: "1e2", want: 10000, valid: true},
		{value: "1e-2", want: 1, valid: true},
		{value: "92233720368547758.07", want: math.MaxInt64, valid: true},
		{value: "92233720368547758.08", valid: false},
		{value: "1.001", valid: false},
		{value: "-1", valid: false},
		{value: "1/2", valid: false},
		{value: "", valid: false},
	}

	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			got, err := ParseCents(test.value)
			if test.valid && err != nil {
				t.Fatalf("ParseCents(%q): %v", test.value, err)
			}
			if !test.valid && err == nil {
				t.Fatalf("ParseCents(%q) unexpectedly succeeded", test.value)
			}
			if got != test.want {
				t.Fatalf("ParseCents(%q) = %d, want %d", test.value, got, test.want)
			}
		})
	}
}

func TestAmountJSON(t *testing.T) {
	tests := []struct {
		amount Amount
		want   string
	}{
		{amount: 0, want: "0"},
		{amount: 4200, want: "42"},
		{amount: 50050, want: "500.5"},
		{amount: 101, want: "1.01"},
		{amount: Amount(math.MaxInt64), want: "92233720368547758.07"},
	}

	for _, test := range tests {
		encoded, err := json.Marshal(test.amount)
		if err != nil {
			t.Fatalf("Marshal(%d): %v", test.amount, err)
		}
		if string(encoded) != test.want {
			t.Fatalf("Marshal(%d) = %s, want %s", test.amount, encoded, test.want)
		}

		var decoded Amount
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("Unmarshal(%s): %v", encoded, err)
		}
		if decoded != test.amount {
			t.Fatalf("decoded amount = %d, want %d", decoded, test.amount)
		}
	}
}
