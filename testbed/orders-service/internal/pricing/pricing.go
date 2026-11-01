// Package pricing turns order lines into a quote. All amounts are integer
// cents; rounding is half up at each step so totals are reproducible.
package pricing

// Discount tiers by quantity of one line. A line of TierSmallQty units or
// more gets SmallBasisPoints off; TierLargeQty or more gets LargeBasisPoints.
const (
	TierSmallQty     = 10
	TierLargeQty     = 50
	SmallBasisPoints = 500  // 5 %
	LargeBasisPoints = 1000 // 10 %
)

// Line is the pricing view of an order line.
type Line struct {
	Quantity  int
	UnitCents int64
}

// Quote is the priced order.
type Quote struct {
	SubtotalCents int64 `json:"subtotal_cents"`
	DiscountCents int64 `json:"discount_cents"`
	TaxCents      int64 `json:"tax_cents"`
	TotalCents    int64 `json:"total_cents"`
}

// DiscountBasisPoints returns the discount tier for a quantity.
func DiscountBasisPoints(quantity int) int {
	switch {
	case quantity >= TierLargeQty:
		return LargeBasisPoints
	case quantity >= TierSmallQty:
		return SmallBasisPoints
	default:
		return 0
	}
}

// applyBasisPoints returns amount * bp / 10000, rounded half up.
func applyBasisPoints(amount int64, bp int) int64 {
	return (amount*int64(bp) + 5000) / 10000
}

// LineSubtotal is quantity times unit price.
func LineSubtotal(l Line) int64 {
	return int64(l.Quantity) * l.UnitCents
}

// LineDiscount is the tier discount of one line.
func LineDiscount(l Line) int64 {
	return applyBasisPoints(LineSubtotal(l), DiscountBasisPoints(l.Quantity))
}

// QuoteFor prices the lines with the given tax rate in basis points. Tax is
// charged on the discounted subtotal.
func QuoteFor(lines []Line, taxBasisPoints int) Quote {
	var q Quote
	for _, l := range lines {
		q.SubtotalCents += LineSubtotal(l)
		q.DiscountCents += LineDiscount(l)
	}
	taxable := q.SubtotalCents - q.DiscountCents
	q.TaxCents = applyBasisPoints(taxable, taxBasisPoints)
	q.TotalCents = taxable + q.TaxCents
	return q
}
