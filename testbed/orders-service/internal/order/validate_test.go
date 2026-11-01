package order

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateLimits(t *testing.T) {
	cases := []struct {
		name string
		edit func(r *NewOrderRequest)
		ok   bool
	}{
		{"valid", func(r *NewOrderRequest) {}, true},
		{"empty customer", func(r *NewOrderRequest) { r.Customer = "  " }, false},
		{"long customer", func(r *NewOrderRequest) { r.Customer = strings.Repeat("x", MaxCustomerLen+1) }, false},
		{"no lines", func(r *NewOrderRequest) { r.Lines = nil }, false},
		{"bad sku", func(r *NewOrderRequest) { r.Lines[0].SKU = "bolt m8" }, false},
		{"repeated sku", func(r *NewOrderRequest) { r.Lines = append(r.Lines, r.Lines[0]) }, false},
		{"quantity too high", func(r *NewOrderRequest) { r.Lines[0].Quantity = MaxQuantity + 1 }, false},
		{"quantity at limit", func(r *NewOrderRequest) { r.Lines[0].Quantity = MaxQuantity }, true},
		{"zero price", func(r *NewOrderRequest) { r.Lines[0].UnitCents = 0 }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := validRequest()
			c.edit(&r)
			err := Validate(r)
			if c.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !c.ok && !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}
