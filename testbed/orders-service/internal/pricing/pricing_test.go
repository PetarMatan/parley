package pricing

import "testing"

func TestDiscountTiers(t *testing.T) {
	cases := map[int]int{1: 0, 9: 0, 11: SmallBasisPoints, 49: SmallBasisPoints, 51: LargeBasisPoints, 500: LargeBasisPoints}
	for qty, want := range cases {
		if got := DiscountBasisPoints(qty); got != want {
			t.Errorf("qty %d: want %d bp, got %d", qty, want, got)
		}
	}
}

func TestRoundingHalfUp(t *testing.T) {
	// 333 cents at 5 % is 16.65, rounds to 17.
	if got := applyBasisPoints(333, 500); got != 17 {
		t.Fatalf("want 17, got %d", got)
	}
	// 1 cent at 5 % is 0.05, rounds to 0.
	if got := applyBasisPoints(1, 500); got != 0 {
		t.Fatalf("want 0, got %d", got)
	}
}

func TestQuoteFor(t *testing.T) {
	lines := []Line{
		{Quantity: 2, UnitCents: 1000}, // 20.00, no discount
		{Quantity: 20, UnitCents: 100}, // 20.00, 5 % = 1.00
		{Quantity: 60, UnitCents: 50},  // 30.00, 10 % = 3.00
	}
	q := QuoteFor(lines, 2000) // 20 % tax
	want := Quote{SubtotalCents: 7000, DiscountCents: 400, TaxCents: 1320, TotalCents: 7920}
	if q != want {
		t.Fatalf("want %+v, got %+v", want, q)
	}
}

func TestQuoteForEmpty(t *testing.T) {
	if q := QuoteFor(nil, 2000); q != (Quote{}) {
		t.Fatalf("want zero quote, got %+v", q)
	}
}
