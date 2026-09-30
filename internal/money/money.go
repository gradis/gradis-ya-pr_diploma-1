package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

var ErrInvalidAmount = errors.New("invalid amount")

type Amount int64

func ParseCents(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") || strings.Contains(value, "/") {
		return 0, ErrInvalidAmount
	}

	amount, ok := new(big.Rat).SetString(value)
	if !ok || amount.Sign() < 0 {
		return 0, ErrInvalidAmount
	}

	amount.Mul(amount, big.NewRat(100, 1))
	if !amount.IsInt() || !amount.Num().IsInt64() {
		return 0, ErrInvalidAmount
	}

	return amount.Num().Int64(), nil
}

func (a Amount) MarshalJSON() ([]byte, error) {
	if a < 0 {
		return nil, ErrInvalidAmount
	}

	return []byte(formatCents(int64(a))), nil
}

func (a *Amount) UnmarshalJSON(data []byte) error {
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return ErrInvalidAmount
	}

	cents, err := ParseCents(number.String())
	if err != nil {
		return err
	}

	*a = Amount(cents)
	return nil
}

func formatCents(cents int64) string {
	whole := cents / 100
	fraction := cents % 100

	switch {
	case fraction == 0:
		return fmt.Sprintf("%d", whole)
	case fraction%10 == 0:
		return fmt.Sprintf("%d.%d", whole, fraction/10)
	default:
		return fmt.Sprintf("%d.%02d", whole, fraction)
	}
}
