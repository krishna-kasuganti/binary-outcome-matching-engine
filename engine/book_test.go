package engine

import (
	"container/list"
	"testing"
)

// helpers

func makeOrder(id, owner string, side Side, price, qty int64) *Order {
	return &Order{
		ID:        id,
		OwnerID:   owner,
		Side:      side,
		Price:     price,
		Quantity:  qty,
		Remaining: qty,
	}
}

func levelFor(ob *OrderBook, side Side, price int) *PriceLevel {
	if side == Buy {
		return ob.bids[price]
	}
	return ob.asks[price]
}

func queueOrders(lvl *PriceLevel) []*Order {
	var out []*Order
	for e := lvl.queue.Front(); e != nil; e = e.Next() {
		out = append(out, e.Value.(*Order))
	}
	return out
}

// ── single order rests at the right level ────────────────────────────────────

func TestRestSingleBid(t *testing.T) {
	ob := NewOrderBook()
	o := makeOrder("o1", "alice", Buy, 42, 10)
	ob.Rest(o)

	lvl := levelFor(ob, Buy, 42)
	if lvl == nil {
		t.Fatal("expected PriceLevel at bids[42], got nil")
	}
	if lvl.TotalVolume != 10 {
		t.Errorf("TotalVolume: want 10, got %d", lvl.TotalVolume)
	}
	if ob.bestBid != 42 {
		t.Errorf("bestBid: want 42, got %d", ob.bestBid)
	}
	if _, ok := ob.lookup["o1"]; !ok {
		t.Error("lookup map missing o1")
	}
	if o.Status != Open {
		t.Errorf("Status: want Open, got %v", o.Status)
	}
	if o.Sequence != 1 {
		t.Errorf("Sequence: want 1, got %d", o.Sequence)
	}
}

func TestRestSingleAsk(t *testing.T) {
	ob := NewOrderBook()
	o := makeOrder("o1", "bob", Sell, 55, 5)
	ob.Rest(o)

	lvl := levelFor(ob, Sell, 55)
	if lvl == nil {
		t.Fatal("expected PriceLevel at asks[55], got nil")
	}
	if lvl.TotalVolume != 5 {
		t.Errorf("TotalVolume: want 5, got %d", lvl.TotalVolume)
	}
	if ob.bestAsk != 55 {
		t.Errorf("bestAsk: want 55, got %d", ob.bestAsk)
	}
}

// ── two orders at the same price: FIFO + summed volume ───────────────────────

func TestRestTwoOrdersSamePrice(t *testing.T) {
	ob := NewOrderBook()
	o1 := makeOrder("o1", "alice", Buy, 50, 10)
	o2 := makeOrder("o2", "bob", Buy, 50, 20)
	ob.Rest(o1)
	ob.Rest(o2)

	lvl := levelFor(ob, Buy, 50)
	if lvl.TotalVolume != 30 {
		t.Errorf("TotalVolume: want 30, got %d", lvl.TotalVolume)
	}

	orders := queueOrders(lvl)
	if len(orders) != 2 {
		t.Fatalf("queue length: want 2, got %d", len(orders))
	}
	// FIFO: o1 arrived first so it has the lower sequence
	if orders[0].Sequence >= orders[1].Sequence {
		t.Errorf("FIFO violated: seq[0]=%d seq[1]=%d", orders[0].Sequence, orders[1].Sequence)
	}
	if orders[0].ID != "o1" || orders[1].ID != "o2" {
		t.Errorf("FIFO order wrong: got %s, %s", orders[0].ID, orders[1].ID)
	}
}

// ── best bid is highest bid, best ask is lowest ask ──────────────────────────

func TestBestCursorsMultiplePrices(t *testing.T) {
	ob := NewOrderBook()
	ob.Rest(makeOrder("b1", "u", Buy, 40, 1))
	ob.Rest(makeOrder("b2", "u", Buy, 45, 1))
	ob.Rest(makeOrder("b3", "u", Buy, 42, 1))
	if ob.bestBid != 45 {
		t.Errorf("bestBid: want 45, got %d", ob.bestBid)
	}

	ob.Rest(makeOrder("a1", "u", Sell, 60, 1))
	ob.Rest(makeOrder("a2", "u", Sell, 55, 1))
	ob.Rest(makeOrder("a3", "u", Sell, 58, 1))
	if ob.bestAsk != 55 {
		t.Errorf("bestAsk: want 55, got %d", ob.bestAsk)
	}
}

// ── cancel a resting order ────────────────────────────────────────────────────

func TestCancelReducesVolume(t *testing.T) {
	ob := NewOrderBook()
	o1 := makeOrder("o1", "u", Buy, 50, 10)
	o2 := makeOrder("o2", "u", Buy, 50, 20)
	ob.Rest(o1)
	ob.Rest(o2)

	if err := ob.Cancel("o1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lvl := levelFor(ob, Buy, 50)
	if lvl.TotalVolume != 20 {
		t.Errorf("TotalVolume after cancel: want 20, got %d", lvl.TotalVolume)
	}
	if _, ok := ob.lookup["o1"]; ok {
		t.Error("o1 should be removed from lookup map")
	}
	if o1.Status != Cancelled {
		t.Errorf("Status: want Cancelled, got %v", o1.Status)
	}
	// o2 still present
	orders := queueOrders(lvl)
	if len(orders) != 1 || orders[0].ID != "o2" {
		t.Errorf("expected only o2 remaining, got %v", orders)
	}
}

func TestCancelLastOrderRecalculatesBestBid(t *testing.T) {
	ob := NewOrderBook()
	ob.Rest(makeOrder("b1", "u", Buy, 40, 5))
	ob.Rest(makeOrder("b2", "u", Buy, 45, 5))

	if ob.bestBid != 45 {
		t.Fatalf("pre-condition: bestBid want 45, got %d", ob.bestBid)
	}

	if err := ob.Cancel("b2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ob.bestBid != 40 {
		t.Errorf("bestBid after cancel: want 40, got %d", ob.bestBid)
	}
	if ob.bids[45] != nil {
		t.Error("bids[45] should be nil after level emptied")
	}
}

func TestCancelLastOrderRecalculatesBestAsk(t *testing.T) {
	ob := NewOrderBook()
	ob.Rest(makeOrder("a1", "u", Sell, 60, 5))
	ob.Rest(makeOrder("a2", "u", Sell, 55, 5))

	if ob.bestAsk != 55 {
		t.Fatalf("pre-condition: bestAsk want 55, got %d", ob.bestAsk)
	}

	if err := ob.Cancel("a2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ob.bestAsk != 60 {
		t.Errorf("bestAsk after cancel: want 60, got %d", ob.bestAsk)
	}
	if ob.asks[55] != nil {
		t.Error("asks[55] should be nil after level emptied")
	}
}

// ── cancel error paths ────────────────────────────────────────────────────────

func TestCancelUnknownID(t *testing.T) {
	ob := NewOrderBook()
	if err := ob.Cancel("ghost"); err != ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestCancelFilledOrder(t *testing.T) {
	ob := NewOrderBook()
	o := makeOrder("o1", "u", Buy, 50, 10)
	ob.Rest(o)

	// Simulate filled externally (as matching would do).
	ob.mu.Lock()
	o.Status = Filled
	// Remove from lookup as matching would, but leave Status Filled to test the guard.
	// Actually we want to test Cancel seeing a Filled order still in the lookup.
	// Re-insert into lookup so Cancel finds it.
	elem := ob.lookup["o1"]
	_ = elem
	ob.mu.Unlock()

	if err := ob.Cancel("o1"); err != ErrAlreadyFilled {
		t.Errorf("want ErrAlreadyFilled, got %v", err)
	}
}

// ── lookup map stores *list.Element, not *Order ───────────────────────────────

func TestLookupStoresListElement(t *testing.T) {
	ob := NewOrderBook()
	o := makeOrder("o1", "u", Buy, 50, 1)
	ob.Rest(o)

	elem, ok := ob.lookup["o1"]
	if !ok {
		t.Fatal("o1 not in lookup")
	}
	// The value must be a *list.Element whose Value is the *Order.
	var _ *list.Element = elem
	if elem.Value.(*Order).ID != "o1" {
		t.Errorf("elem.Value is not the expected order")
	}
}
