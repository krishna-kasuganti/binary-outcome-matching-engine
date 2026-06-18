package engine

import "testing"

// submitOrder is a thin helper that pre-sets Remaining = Quantity.
func submitOrder(ob *OrderBook, id, owner string, side Side, price, qty int64) (*Order, error) {
	o := &Order{
		ID:        id,
		OwnerID:   owner,
		Side:      side,
		Price:     price,
		Quantity:  qty,
		Remaining: qty,
	}
	_, err := ob.Submit(o)
	return o, err
}

// restOrder is the same helper for pre-populating the book without matching.
func restOrder(ob *OrderBook, id, owner string, side Side, price, qty int64) *Order {
	o := &Order{
		ID:        id,
		OwnerID:   owner,
		Side:      side,
		Price:     price,
		Quantity:  qty,
		Remaining: qty,
	}
	ob.Rest(o)
	return o
}

// ── full match ────────────────────────────────────────────────────────────────

func TestFullMatch(t *testing.T) {
	ob := NewOrderBook()
	sell := restOrder(ob, "s1", "alice", Sell, 50, 10)

	buy, err := submitOrder(ob, "b1", "bob", Buy, 50, 10)
	if err != nil {
		t.Fatalf("submit error: %v", err)
	}

	if buy.Status != Filled {
		t.Errorf("buy status: want Filled, got %v", buy.Status)
	}
	if sell.Status != Filled {
		t.Errorf("sell status: want Filled, got %v", sell.Status)
	}

	tape := ob.Tape()
	if len(tape) != 1 {
		t.Fatalf("tape length: want 1, got %d", len(tape))
	}
	tr := tape[0]
	if tr.Price != 50 {
		t.Errorf("trade price: want 50, got %d", tr.Price)
	}
	if tr.Quantity != 10 {
		t.Errorf("trade qty: want 10, got %d", tr.Quantity)
	}
	if tr.MakerOrderID != "s1" || tr.TakerOrderID != "b1" {
		t.Errorf("trade roles wrong: maker=%s taker=%s", tr.MakerOrderID, tr.TakerOrderID)
	}
}

// ── aggressor partial: buy is larger, sell fills, buy rests remainder ─────────

func TestAggressorPartial(t *testing.T) {
	ob := NewOrderBook()
	sell := restOrder(ob, "s1", "alice", Sell, 50, 5)

	buy, _ := submitOrder(ob, "b1", "bob", Buy, 50, 10)

	if sell.Status != Filled {
		t.Errorf("sell status: want Filled, got %v", sell.Status)
	}
	if buy.Remaining != 5 {
		t.Errorf("buy.Remaining: want 5, got %d", buy.Remaining)
	}
	if buy.Status != PartiallyFilled {
		t.Errorf("buy status: want PartiallyFilled, got %v", buy.Status)
	}

	// Buy remainder must be resting in the book.
	lvl := ob.bids[50]
	if lvl == nil || lvl.TotalVolume != 5 {
		t.Errorf("bids[50].TotalVolume: want 5, got %v", func() int64 {
			if lvl == nil {
				return -1
			}
			return lvl.TotalVolume
		}())
	}
	if _, ok := ob.lookup["b1"]; !ok {
		t.Error("b1 should be in lookup map after resting remainder")
	}

	tape := ob.Tape()
	if len(tape) != 1 || tape[0].Quantity != 5 {
		t.Errorf("tape: want 1 trade of qty 5, got %v", tape)
	}
}

// ── resting partial: buy is smaller, sell stays at front ─────────────────────

func TestRestingPartial(t *testing.T) {
	ob := NewOrderBook()
	sell := restOrder(ob, "s1", "alice", Sell, 50, 10)

	buy, _ := submitOrder(ob, "b1", "bob", Buy, 50, 5)

	if buy.Status != Filled {
		t.Errorf("buy status: want Filled, got %v", buy.Status)
	}
	if sell.Status != PartiallyFilled {
		t.Errorf("sell status: want PartiallyFilled, got %v", sell.Status)
	}
	if sell.Remaining != 5 {
		t.Errorf("sell.Remaining: want 5, got %d", sell.Remaining)
	}

	// Sell must still be at the front of asks[50].
	lvl := ob.asks[50]
	if lvl == nil {
		t.Fatal("asks[50] should still exist")
	}
	front := lvl.queue.Front().Value.(*Order)
	if front.ID != "s1" {
		t.Errorf("front of asks[50]: want s1, got %s", front.ID)
	}
	if lvl.TotalVolume != 5 {
		t.Errorf("asks[50].TotalVolume: want 5, got %d", lvl.TotalVolume)
	}
}

// ── no cross: buy below best ask rests with no trades ────────────────────────

func TestNoCross(t *testing.T) {
	ob := NewOrderBook()
	restOrder(ob, "s1", "alice", Sell, 60, 10)

	buy, _ := submitOrder(ob, "b1", "bob", Buy, 50, 5)

	if len(ob.Tape()) != 0 {
		t.Errorf("expected no trades, got %d", len(ob.Tape()))
	}
	if buy.Status != Open {
		t.Errorf("buy status: want Open, got %v", buy.Status)
	}
	if ob.bestBid != 50 {
		t.Errorf("bestBid: want 50, got %d", ob.bestBid)
	}
	if ob.bestAsk != 60 {
		t.Errorf("bestAsk unchanged: want 60, got %d", ob.bestAsk)
	}
}

// ── multi-level sweep: buy clears several ask levels in price order ───────────

func TestMultiLevelSweep(t *testing.T) {
	ob := NewOrderBook()
	restOrder(ob, "s1", "alice", Sell, 50, 5)
	restOrder(ob, "s2", "alice", Sell, 51, 5)
	restOrder(ob, "s3", "alice", Sell, 52, 5)

	buy, _ := submitOrder(ob, "b1", "bob", Buy, 55, 15)

	if buy.Status != Filled {
		t.Errorf("buy status: want Filled, got %v", buy.Status)
	}

	tape := ob.Tape()
	if len(tape) != 3 {
		t.Fatalf("tape length: want 3, got %d", len(tape))
	}
	wantPrices := []int64{50, 51, 52}
	for i, tr := range tape {
		if tr.Price != wantPrices[i] {
			t.Errorf("trade[%d] price: want %d, got %d", i, wantPrices[i], tr.Price)
		}
		if tr.Quantity != 5 {
			t.Errorf("trade[%d] qty: want 5, got %d", i, tr.Quantity)
		}
	}

	// All ask levels should be cleared.
	for _, p := range []int{50, 51, 52} {
		if ob.asks[p] != nil {
			t.Errorf("asks[%d] should be nil after sweep", p)
		}
	}
	if ob.bestAsk != 0 {
		t.Errorf("bestAsk: want 0, got %d", ob.bestAsk)
	}
}

// ── time priority: earlier sequence fills first at same price ────────────────

func TestTimePriority(t *testing.T) {
	ob := NewOrderBook()
	sell1 := restOrder(ob, "s1", "alice", Sell, 50, 10)
	sell2 := restOrder(ob, "s2", "carol", Sell, 50, 10)

	_, _ = submitOrder(ob, "b1", "bob", Buy, 50, 10)

	if sell1.Status != Filled {
		t.Errorf("sell1 (earlier seq) should be Filled, got %v", sell1.Status)
	}
	if sell2.Status == Filled {
		t.Errorf("sell2 (later seq) should not be Filled yet")
	}

	tape := ob.Tape()
	if len(tape) != 1 || tape[0].MakerOrderID != "s1" {
		t.Errorf("trade should have maker=s1, got %v", tape)
	}
}

// ── maker price: trade prints at resting price, not incoming price ────────────

func TestMakerPrice(t *testing.T) {
	ob := NewOrderBook()
	restOrder(ob, "s1", "alice", Sell, 55, 5)

	_, _ = submitOrder(ob, "b1", "bob", Buy, 60, 5)

	tape := ob.Tape()
	if len(tape) != 1 {
		t.Fatalf("tape length: want 1, got %d", len(tape))
	}
	if tape[0].Price != 55 {
		t.Errorf("trade price: want 55 (resting), got %d", tape[0].Price)
	}
}

// ── drift check: partial fill then cancel, TotalVolume must reach 0 ──────────

func TestDriftCheck(t *testing.T) {
	ob := NewOrderBook()
	restOrder(ob, "s1", "alice", Sell, 50, 100)

	// Partial fill: buy 40.
	_, _ = submitOrder(ob, "b1", "bob", Buy, 50, 40)

	lvl := ob.asks[50]
	if lvl == nil {
		t.Fatal("asks[50] should still exist after partial fill")
	}
	if lvl.TotalVolume != 60 {
		t.Errorf("TotalVolume after partial fill: want 60, got %d", lvl.TotalVolume)
	}

	// Cancel the resting sell remainder.
	if err := ob.Cancel("s1"); err != nil {
		t.Fatalf("cancel error: %v", err)
	}

	if ob.asks[50] != nil {
		t.Errorf("asks[50] should be nil after cancel empties the level")
	}
	if ob.bestAsk != 0 {
		t.Errorf("bestAsk: want 0, got %d", ob.bestAsk)
	}
}
