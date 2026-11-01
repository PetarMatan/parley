package order

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Limits of a valid order. They are part of the API contract (README).
const (
	MaxCustomerLen = 80
	MaxLines       = 100
	MaxQuantity    = 1000
	MaxUnitCents   = 10_000_000 // 100 000.00 in the order currency
)

var skuPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{2,31}$`)

// ErrInvalid wraps every validation failure so callers can map it to 400.
var ErrInvalid = errors.New("invalid order")

// Validate checks a request against the limits. It returns the first problem
// found, wrapped in ErrInvalid.
func Validate(r NewOrderRequest) error {
	customer := strings.TrimSpace(r.Customer)
	if customer == "" {
		return fmt.Errorf("%w: customer is required", ErrInvalid)
	}
	if len(customer) > MaxCustomerLen {
		return fmt.Errorf("%w: customer longer than %d characters", ErrInvalid, MaxCustomerLen)
	}
	if len(r.Lines) == 0 {
		return fmt.Errorf("%w: at least one line is required", ErrInvalid)
	}
	if len(r.Lines) > MaxLines {
		return fmt.Errorf("%w: more than %d lines", ErrInvalid, MaxLines)
	}
	seen := make(map[string]bool, len(r.Lines))
	for i, l := range r.Lines {
		if !skuPattern.MatchString(l.SKU) {
			return fmt.Errorf("%w: line %d: sku %q is not 3 to 32 upper-case letters, digits or dashes", ErrInvalid, i, l.SKU)
		}
		if seen[l.SKU] {
			return fmt.Errorf("%w: line %d: sku %s repeated", ErrInvalid, i, l.SKU)
		}
		seen[l.SKU] = true
		if l.Quantity < 1 || l.Quantity > MaxQuantity {
			return fmt.Errorf("%w: line %d: quantity must be 1 to %d", ErrInvalid, i, MaxQuantity)
		}
		if l.UnitCents < 1 || l.UnitCents > MaxUnitCents {
			return fmt.Errorf("%w: line %d: unit_cents must be 1 to %d", ErrInvalid, i, MaxUnitCents)
		}
	}
	return nil
}
