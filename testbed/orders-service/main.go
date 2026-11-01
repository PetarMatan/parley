// Command orders-service serves a small order API over HTTP. It keeps orders in
// memory and exists to give a code review something real to look at.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"gitlab.parleylab.dev/parley-lab/orders-service/internal/api"
	"gitlab.parleylab.dev/parley-lab/orders-service/internal/order"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	store := order.NewStore()
	srv := &http.Server{
		Addr:              addr,
		Handler:           api.New(store, api.DefaultTaxBasisPoints),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("orders-service listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}
