// Package api exposes the order store over HTTP with JSON bodies.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"gitlab.parleylab.dev/parley-lab/orders-service/internal/order"
	"gitlab.parleylab.dev/parley-lab/orders-service/internal/pricing"
)

// DefaultTaxBasisPoints is the tax rate used when none is configured.
const DefaultTaxBasisPoints = 2000

// maxBodyBytes bounds a request body; orders are small.
const maxBodyBytes = 64 << 10

// Server holds the handler dependencies.
type Server struct {
	store *order.Store
	tax   int
	mux   *http.ServeMux
}

// orderResponse is an order with its quote.
type orderResponse struct {
	*order.Order
	Quote pricing.Quote `json:"quote"`
}

// New builds the HTTP handler.
func New(store *order.Store, taxBasisPoints int) http.Handler {
	s := &Server{store: store, tax: taxBasisPoints, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /orders", s.createOrder)
	s.mux.HandleFunc("GET /orders", s.listOrders)
	s.mux.HandleFunc("GET /orders/{id}", s.getOrder)
	s.mux.HandleFunc("POST /orders/{id}/cancel", s.cancelOrder)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return s.mux
}

func (s *Server) respond(o *order.Order) orderResponse {
	lines := make([]pricing.Line, len(o.Lines))
	for i, l := range o.Lines {
		lines[i] = pricing.Line{Quantity: l.Quantity, UnitCents: l.UnitCents}
	}
	return orderResponse{Order: o, Quote: pricing.QuoteFor(lines, s.tax)}
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	var req order.NewOrderRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "body is not a valid order: "+err.Error())
		return
	}
	o, err := s.store.Create(req)
	if err != nil {
		if errors.Is(err, order.ErrInvalid) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create order")
		return
	}
	w.Header().Set("Location", "/orders/"+o.ID)
	writeJSON(w, http.StatusCreated, s.respond(o))
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	orders := s.store.List(r.URL.Query().Get("customer"))
	out := make([]orderResponse, len(orders))
	for i, o := range orders {
		out[i] = s.respond(o)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	o, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.respond(o))
}

func (s *Server) cancelOrder(w http.ResponseWriter, r *http.Request) {
	o, err := s.store.Cancel(r.PathValue("id"))
	switch {
	case errors.Is(err, order.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, order.ErrAlreadyCancelled):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not cancel order")
	default:
		writeJSON(w, http.StatusOK, s.respond(o))
	}
}
