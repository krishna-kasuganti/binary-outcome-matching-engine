package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"matching-engine/engine"
)

// ── test helpers ──────────────────────────────────────────────────────────────

func newTestServer() (*engine.OrderBook, http.Handler) {
	ob := engine.NewOrderBook()
	return ob, NewServer(ob)
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(method, path, r))
	return rr
}

func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&m); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	return m
}

// ── POST /orders — valid shape ────────────────────────────────────────────────

func TestPostOrderValidShape(t *testing.T) {
	_, h := newTestServer()
	rr := do(h, "POST", "/orders", `{"owner_id":"alice","side":"buy","price":50,"quantity":10}`)

	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	resp := decodeJSON(t, rr)

	for _, key := range []string{"order_id", "status", "remaining", "fills"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("response missing field %q", key)
		}
	}
	if resp["status"] != "open" {
		t.Errorf("status: want open, got %v", resp["status"])
	}
	if resp["remaining"] != float64(10) {
		t.Errorf("remaining: want 10, got %v", resp["remaining"])
	}
	fills, ok := resp["fills"].([]any)
	if !ok || len(fills) != 0 {
		t.Errorf("fills: want empty array, got %v", resp["fills"])
	}
	if id, _ := resp["order_id"].(string); id == "" {
		t.Error("order_id must be a non-empty string")
	}
}

// ── POST /orders — validation (table-driven) ──────────────────────────────────

func TestPostOrderValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"price 0", `{"owner_id":"alice","side":"buy","price":0,"quantity":10}`},
		{"price 100", `{"owner_id":"alice","side":"buy","price":100,"quantity":10}`},
		{"quantity 0", `{"owner_id":"alice","side":"buy","price":50,"quantity":0}`},
		{"missing owner", `{"owner_id":"","side":"buy","price":50,"quantity":10}`},
		{"bad side", `{"owner_id":"alice","side":"both","price":50,"quantity":10}`},
		{"malformed JSON", `{not json`},
	}

	_, h := newTestServer()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := do(h, "POST", "/orders", tc.body)
			if rr.Code != 400 {
				t.Errorf("want 400, got %d: %s", rr.Code, rr.Body)
			}
			resp := decodeJSON(t, rr)
			if _, ok := resp["error"]; !ok {
				t.Errorf("response missing \"error\" field: %v", resp)
			}
		})
	}
}

// ── POST /orders — crossing fills ────────────────────────────────────────────

func TestPostOrderCross(t *testing.T) {
	ob, h := newTestServer()
	ob.Rest(&engine.Order{
		ID: "s1", OwnerID: "alice", Side: engine.Sell, Price: 50, Quantity: 5, Remaining: 5,
	})

	rr := do(h, "POST", "/orders", `{"owner_id":"bob","side":"buy","price":50,"quantity":5}`)
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	resp := decodeJSON(t, rr)

	fills, ok := resp["fills"].([]any)
	if !ok || len(fills) != 1 {
		t.Fatalf("fills: want 1 entry, got %v", resp["fills"])
	}
	fill := fills[0].(map[string]any)
	if fill["price"] != float64(50) {
		t.Errorf("fill price: want 50, got %v", fill["price"])
	}
	if fill["quantity"] != float64(5) {
		t.Errorf("fill quantity: want 5, got %v", fill["quantity"])
	}
	if fill["maker_order_id"] != "s1" {
		t.Errorf("fill maker_order_id: want s1, got %v", fill["maker_order_id"])
	}
	if resp["status"] != "filled" {
		t.Errorf("status: want filled, got %v", resp["status"])
	}
	if resp["remaining"] != float64(0) {
		t.Errorf("remaining: want 0, got %v", resp["remaining"])
	}
}

// ── DELETE /orders/{id} ──────────────────────────────────────────────────────

func TestDeleteOrder(t *testing.T) {
	ob, h := newTestServer()

	// POST a resting order; extract its server-assigned ID.
	rr := do(h, "POST", "/orders", `{"owner_id":"alice","side":"buy","price":30,"quantity":5}`)
	postResp := decodeJSON(t, rr)
	id := postResp["order_id"].(string)

	// Cancel it — 200.
	rr = do(h, "DELETE", "/orders/"+id, "")
	if rr.Code != 200 {
		t.Errorf("cancel resting: want 200, got %d: %s", rr.Code, rr.Body)
	}
	resp := decodeJSON(t, rr)
	if resp["message"] != "cancelled" {
		t.Errorf("message: want cancelled, got %v", resp["message"])
	}

	// Cancel unknown ID — 404.
	rr = do(h, "DELETE", "/orders/ghost", "")
	if rr.Code != 404 {
		t.Errorf("cancel unknown: want 404, got %d", rr.Code)
	}

	// Cancel a Filled order — 409.
	// Rest an order, then mark it Filled in-place (it stays in the lookup map,
	// triggering the ErrAlreadyFilled guard in Cancel).
	filled := &engine.Order{
		ID: "filled-x", OwnerID: "alice", Side: engine.Buy, Price: 50, Quantity: 10, Remaining: 10,
	}
	ob.Rest(filled)
	filled.Status = engine.Filled

	rr = do(h, "DELETE", "/orders/filled-x", "")
	if rr.Code != 409 {
		t.Errorf("cancel filled: want 409, got %d: %s", rr.Code, rr.Body)
	}
}

// ── GET /orders/{id} ─────────────────────────────────────────────────────────

func TestGetOrder(t *testing.T) {
	_, h := newTestServer()

	// POST a resting sell; capture ID.
	rr := do(h, "POST", "/orders", `{"owner_id":"alice","side":"sell","price":60,"quantity":7}`)
	id := decodeJSON(t, rr)["order_id"].(string)

	// GET it — 200.
	rr = do(h, "GET", "/orders/"+id, "")
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	resp := decodeJSON(t, rr)
	if resp["quantity"] != float64(7) {
		t.Errorf("quantity: want 7, got %v", resp["quantity"])
	}
	if resp["remaining"] != float64(7) {
		t.Errorf("remaining: want 7, got %v", resp["remaining"])
	}
	if resp["status"] != "open" {
		t.Errorf("status: want open, got %v", resp["status"])
	}

	// GET unknown ID — 404.
	rr = do(h, "GET", "/orders/ghost", "")
	if rr.Code != 404 {
		t.Errorf("get unknown: want 404, got %d", rr.Code)
	}
}

// ── GET /orderbook — sort order and depth ────────────────────────────────────

func TestGetOrderBook(t *testing.T) {
	ob, h := newTestServer()

	for _, o := range []*engine.Order{
		{ID: "b40", OwnerID: "alice", Side: engine.Buy, Price: 40, Quantity: 5, Remaining: 5},
		{ID: "b41", OwnerID: "alice", Side: engine.Buy, Price: 41, Quantity: 5, Remaining: 5},
		{ID: "b42", OwnerID: "alice", Side: engine.Buy, Price: 42, Quantity: 5, Remaining: 5},
		{ID: "a50", OwnerID: "alice", Side: engine.Sell, Price: 50, Quantity: 5, Remaining: 5},
		{ID: "a51", OwnerID: "alice", Side: engine.Sell, Price: 51, Quantity: 5, Remaining: 5},
		{ID: "a52", OwnerID: "alice", Side: engine.Sell, Price: 52, Quantity: 5, Remaining: 5},
	} {
		ob.Rest(o)
	}

	rr := do(h, "GET", "/orderbook", "")
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	resp := decodeJSON(t, rr)

	// best_bid and best_ask
	if resp["best_bid"] != float64(42) {
		t.Errorf("best_bid: want 42, got %v", resp["best_bid"])
	}
	if resp["best_ask"] != float64(50) {
		t.Errorf("best_ask: want 50, got %v", resp["best_ask"])
	}

	// Bids sorted high-to-low: 42, 41, 40
	bids := resp["bids"].([]any)
	if len(bids) != 3 {
		t.Fatalf("bids length: want 3, got %d", len(bids))
	}
	for i, wantPrice := range []float64{42, 41, 40} {
		p := bids[i].(map[string]any)["price"].(float64)
		if p != wantPrice {
			t.Errorf("bids[%d].price: want %v, got %v", i, wantPrice, p)
		}
	}

	// Asks sorted low-to-high: 50, 51, 52
	asks := resp["asks"].([]any)
	if len(asks) != 3 {
		t.Fatalf("asks length: want 3, got %d", len(asks))
	}
	for i, wantPrice := range []float64{50, 51, 52} {
		p := asks[i].(map[string]any)["price"].(float64)
		if p != wantPrice {
			t.Errorf("asks[%d].price: want %v, got %v", i, wantPrice, p)
		}
	}

	// depth=2 caps each side to 2 levels from the top
	rr = do(h, "GET", "/orderbook?depth=2", "")
	resp = decodeJSON(t, rr)
	if len(resp["bids"].([]any)) != 2 {
		t.Errorf("depth=2 bids: want 2, got %d", len(resp["bids"].([]any)))
	}
	if len(resp["asks"].([]any)) != 2 {
		t.Errorf("depth=2 asks: want 2, got %d", len(resp["asks"].([]any)))
	}
	// depth=2 bids top two are 42 and 41
	if resp["bids"].([]any)[0].(map[string]any)["price"].(float64) != 42 {
		t.Errorf("depth=2 bids[0].price: want 42")
	}
	// depth=2 asks top two are 50 and 51
	if resp["asks"].([]any)[0].(map[string]any)["price"].(float64) != 50 {
		t.Errorf("depth=2 asks[0].price: want 50")
	}
}

// ── GET /trades ───────────────────────────────────────────────────────────────

func TestGetTrades(t *testing.T) {
	ob, h := newTestServer()

	ob.Rest(&engine.Order{
		ID: "s1", OwnerID: "alice", Side: engine.Sell, Price: 50, Quantity: 10, Remaining: 10,
	})
	do(h, "POST", "/orders", `{"owner_id":"bob","side":"buy","price":50,"quantity":10}`)

	rr := do(h, "GET", "/trades", "")
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d", rr.Code)
	}

	var trades []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&trades); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("trades: want 1, got %d", len(trades))
	}
	tr := trades[0]
	if tr["price"] != float64(50) {
		t.Errorf("price: want 50, got %v", tr["price"])
	}
	if tr["quantity"] != float64(10) {
		t.Errorf("quantity: want 10, got %v", tr["quantity"])
	}
	if tr["maker_order_id"] != "s1" {
		t.Errorf("maker_order_id: want s1, got %v", tr["maker_order_id"])
	}
	if tr["maker_owner"] != "alice" {
		t.Errorf("maker_owner: want alice, got %v", tr["maker_owner"])
	}
	if tr["taker_owner"] != "bob" {
		t.Errorf("taker_owner: want bob, got %v", tr["taker_owner"])
	}
	// sequence must be a positive number
	if seq, _ := tr["sequence"].(float64); seq < 1 {
		t.Errorf("sequence: want >= 1, got %v", tr["sequence"])
	}
}
