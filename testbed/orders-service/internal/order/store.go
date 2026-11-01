package order

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Errors returned by the store.
var (
	ErrNotFound         = errors.New("order not found")
	ErrAlreadyCancelled = errors.New("order already cancelled")
)

// Store keeps orders in memory. It is safe for concurrent use.
type Store struct {
	mu     sync.RWMutex
	seq    int
	orders map[string]*Order
	now    func() time.Time
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{orders: make(map[string]*Order), now: time.Now}
}

// Create validates the request, assigns an id and stores the order.
func (s *Store) Create(r NewOrderRequest) (*Order, error) {
	if err := Validate(r); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	o := &Order{
		ID:        fmt.Sprintf("ord-%06d", s.seq),
		Customer:  strings.TrimSpace(r.Customer),
		Lines:     append([]Line(nil), r.Lines...),
		Status:    StatusOpen,
		CreatedAt: s.now().UTC(),
	}
	s.orders[o.ID] = o
	return o.clone(), nil
}

// Get returns a copy of the order with the given id.
func (s *Store) Get(id string) (*Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orders[id]
	if !ok {
		return nil, ErrNotFound
	}
	return o.clone(), nil
}

// List returns copies of all orders, oldest first. An empty customer
// matches every order.
func (s *Store) List(customer string) []*Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Order, 0, len(s.orders))
	for _, o := range s.orders {
		if customer != "" && o.Customer != customer {
			continue
		}
		out = append(out, o.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Cancel moves an open order to cancelled. Cancelling twice is an error.
func (s *Store) Cancel(id string) (*Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return nil, ErrNotFound
	}
	if o.Status == StatusCancelled {
		return nil, ErrAlreadyCancelled
	}
	o.Status = StatusCancelled
	return o.clone(), nil
}
