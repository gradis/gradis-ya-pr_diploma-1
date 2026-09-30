package validator

import (
	"strings"
	"testing"
)

func TestValidOrderNumber(t *testing.T) {
	tests := []struct {
		name   string
		number string
		valid  bool
	}{
		{name: "specification example", number: "12345678903", valid: true},
		{name: "withdrawal example", number: "2377225624", valid: true},
		{name: "single zero", number: "0", valid: true},
		{name: "wrong checksum", number: "12345678904", valid: false},
		{name: "letters", number: "123x", valid: false},
		{name: "empty", number: "", valid: false},
		{name: "too long", number: strings.Repeat("0", MaxOrderNumberLength+1), valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ValidOrderNumber(test.number); got != test.valid {
				t.Fatalf("ValidOrderNumber(%q) = %v, want %v", test.number, got, test.valid)
			}
		})
	}
}
