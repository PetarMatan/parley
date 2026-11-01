// Package order holds the order model, its validation rules and the in-memory
// store.
package order

import "time"

// Status of an order. An order starts open and can only move to cancelled.
type Status string

const (
	StatusOpen      Status = "open"
	StatusCancelled Status = "cancelled"
)

// Line is one product position of an order. Prices are integer cents.
type Line struct {
	SKU       string `json:"sku"`
	Quantity  int    `json:"quantity"`
	UnitCents int64  `json:"unit_cents"`
}

// Order is what the store keeps and the API returns.
type Order struct {
	ID        string    `json:"id"`
	Customer  string    `json:"customer"`
	Lines     []Line    `json:"lines"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// NewOrderRequest is the body of POST /orders.
type NewOrderRequest struct {
	Customer string `json:"customer"`
	Lines    []Line `json:"lines"`
}

// clone returns a copy whose line slice does not alias the stored one.
func (o *Order) clone() *Order {
	c := *o
	c.Lines = append([]Line(nil), o.Lines...)
	return &c
}
