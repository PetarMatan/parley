package order

import (
	"errors"
	"testing"
	"time"
)

func validRequest() NewOrderRequest {
	return NewOrderRequest{Customer: "acme", Lines: []Line{{SKU: "BOLT-M8", Quantity: 4, UnitCents: 125}}}
}

func TestCreateAssignsIDAndTimestamp(t *testing.T) {
	s := NewStore()
	fixed := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }
	o, err := s.Create(validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if o.ID != "ord-000001" || o.Status != StatusOpen || !o.CreatedAt.Equal(fixed) {
		t.Fatalf("unexpected order %+v", o)
	}
}

func TestCreateRejectsInvalid(t *testing.T) {
	s := NewStore()
	r := validRequest()
	r.Lines[0].Quantity = 0
	if _, err := s.Create(r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestGetReturnsCopy(t *testing.T) {
	s := NewStore()
	o, _ := s.Create(validRequest())
	got, err := s.Get(o.ID)
	if err != nil {
		t.Fatal(err)
	}
	got.Lines[0].Quantity = 99
	again, _ := s.Get(o.ID)
	if again.Lines[0].Quantity != 4 {
		t.Fatal("Get leaked the stored line slice")
	}
}

func TestGetUnknown(t *testing.T) {
	if _, err := NewStore().Get("ord-nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListFiltersAndSorts(t *testing.T) {
	s := NewStore()
	a := validRequest()
	b := validRequest()
	b.Customer = "globex"
	s.Create(a)
	s.Create(b)
	s.Create(a)
	all := s.List("")
	if len(all) != 3 || all[0].ID != "ord-000001" || all[2].ID != "ord-000003" {
		t.Fatalf("unexpected list %+v", all)
	}
	if got := s.List("globex"); len(got) != 1 || got[0].ID != "ord-000002" {
		t.Fatalf("unexpected filtered list %+v", got)
	}
}

func TestCancelOnce(t *testing.T) {
	s := NewStore()
	o, _ := s.Create(validRequest())
	c, err := s.Cancel(o.ID)
	if err != nil || c.Status != StatusCancelled {
		t.Fatalf("first cancel: %v %+v", err, c)
	}
	if _, err := s.Cancel(o.ID); !errors.Is(err, ErrAlreadyCancelled) {
		t.Fatalf("second cancel: want ErrAlreadyCancelled, got %v", err)
	}
	if _, err := s.Cancel("ord-nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown cancel: want ErrNotFound, got %v", err)
	}
}
