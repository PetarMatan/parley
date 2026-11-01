package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitlab.parleylab.dev/parley-lab/orders-service/internal/order"
)

const validBody = `{"customer":"acme","lines":[{"sku":"BOLT-M8","quantity":20,"unit_cents":100}]}`

func newTestServer() http.Handler {
	return New(order.NewStore(), DefaultTaxBasisPoints)
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCreateAndGet(t *testing.T) {
	h := newTestServer()
	rec := do(h, http.MethodPost, "/orders", validBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d %s", rec.Code, rec.Body)
	}
	var created orderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	// 20 x 1.00 = 20.00, 5 % off = 1.00, 20 % tax on 19.00 = 3.80, total 22.80
	if created.Quote.TotalCents != 2280 || created.Quote.DiscountCents != 100 {
		t.Fatalf("unexpected quote %+v", created.Quote)
	}
	if got := rec.Header().Get("Location"); got != "/orders/"+created.ID {
		t.Fatalf("unexpected Location %q", got)
	}
	rec = do(h, http.MethodGet, "/orders/"+created.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d", rec.Code)
	}
}

func TestCreateRejectsBadBodies(t *testing.T) {
	h := newTestServer()
	for _, body := range []string{
		`not json`,
		`{"customer":"","lines":[]}`,
		`{"customer":"acme","lines":[{"sku":"BOLT-M8","quantity":0,"unit_cents":100}]}`,
		`{"customer":"acme","extra":1,"lines":[{"sku":"BOLT-M8","quantity":1,"unit_cents":100}]}`,
	} {
		if rec := do(h, http.MethodPost, "/orders", body); rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: want 400, got %d", body, rec.Code)
		}
	}
}

func TestGetUnknownIs404(t *testing.T) {
	if rec := do(newTestServer(), http.MethodGet, "/orders/ord-nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestListFilters(t *testing.T) {
	h := newTestServer()
	do(h, http.MethodPost, "/orders", validBody)
	do(h, http.MethodPost, "/orders", strings.Replace(validBody, "acme", "globex", 1))
	var all, some []orderResponse
	json.Unmarshal(do(h, http.MethodGet, "/orders", "").Body.Bytes(), &all)
	json.Unmarshal(do(h, http.MethodGet, "/orders?customer=globex", "").Body.Bytes(), &some)
	if len(all) != 2 || len(some) != 1 || some[0].Customer != "globex" {
		t.Fatalf("unexpected lists: all=%d some=%d", len(all), len(some))
	}
}

func TestCancelTwice(t *testing.T) {
	h := newTestServer()
	var created orderResponse
	json.Unmarshal(do(h, http.MethodPost, "/orders", validBody).Body.Bytes(), &created)
	if rec := do(h, http.MethodPost, "/orders/"+created.ID+"/cancel", ""); rec.Code != http.StatusOK {
		t.Fatalf("first cancel: want 200, got %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/orders/"+created.ID+"/cancel", ""); rec.Code != http.StatusConflict {
		t.Fatalf("second cancel: want 409, got %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/orders/ord-nope/cancel", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown cancel: want 404, got %d", rec.Code)
	}
}
