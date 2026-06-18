package engine

import "testing"

// TestSTPSkipAndContinue: own order at front of level is skipped; the order
// behind it from a different owner fills normally.
func TestSTPSkipAndContinue(t *testing.T) {
	ob := NewOrderBook()

	// Two Sells at the same price: A first (will be skipped), B second (will fill).
	ownSell := restOrder(ob, "s_a1", "alice", Sell, 50, 5)
	otherSell := restOrder(ob, "s_b1", "bob", Sell, 50, 5)

	volumeBefore := ob.asks[50].TotalVolume // 10

	buy, _ := submitOrder(ob, "b1", "alice", Buy, 50, 10)

	// Own Sell must be completely untouched.
	if ownSell.Remaining != 5 {
		t.Errorf("own sell Remaining: want 5, got %d", ownSell.Remaining)
	}
	if ownSell.Status != Open {
		t.Errorf("own sell Status: want Open, got %v", ownSell.Status)
	}

	// Other Sell must be fully consumed.
	if otherSell.Status != Filled {
		t.Errorf("other sell Status: want Filled, got %v", otherSell.Status)
	}

	// Exactly one trade, at the resting price.
	tape := ob.Tape()
	if len(tape) != 1 {
		t.Fatalf("tape length: want 1, got %d", len(tape))
	}
	if tape[0].Price != 50 {
		t.Errorf("trade price: want 50, got %d", tape[0].Price)
	}
	if tape[0].MakerOrderID != "s_b1" {
		t.Errorf("trade maker: want s_b1, got %s", tape[0].MakerOrderID)
	}

	// TotalVolume dropped only by the filled quantity (5), not by the skip (5).
	wantVol := volumeBefore - 5
	if ob.asks[50].TotalVolume != wantVol {
		t.Errorf("asks[50].TotalVolume: want %d, got %d", wantVol, ob.asks[50].TotalVolume)
	}

	// Buy rested its unfilled remainder.
	if buy.Status != PartiallyFilled {
		t.Errorf("buy Status: want PartiallyFilled, got %v", buy.Status)
	}
	if buy.Remaining != 5 {
		t.Errorf("buy Remaining: want 5, got %d", buy.Remaining)
	}
}

// TestSTPOnlyOwnLiquidity: every opposing order belongs to the same owner.
// Zero trades, incoming rests with full Remaining, all own Sells untouched.
func TestSTPOnlyOwnLiquidity(t *testing.T) {
	ob := NewOrderBook()

	own1 := restOrder(ob, "s_a1", "alice", Sell, 50, 5)
	own2 := restOrder(ob, "s_a2", "alice", Sell, 50, 5)

	volBefore := ob.asks[50].TotalVolume // 10

	buy, _ := submitOrder(ob, "b1", "alice", Buy, 50, 10)

	if len(ob.Tape()) != 0 {
		t.Errorf("expected 0 trades, got %d", len(ob.Tape()))
	}
	if buy.Status != Open {
		t.Errorf("buy Status: want Open, got %v", buy.Status)
	}
	if buy.Remaining != 10 {
		t.Errorf("buy Remaining: want 10, got %d", buy.Remaining)
	}
	if own1.Remaining != 5 || own1.Status != Open {
		t.Errorf("own1: want Remaining=5/Open, got Remaining=%d/%v", own1.Remaining, own1.Status)
	}
	if own2.Remaining != 5 || own2.Status != Open {
		t.Errorf("own2: want Remaining=5/Open, got Remaining=%d/%v", own2.Remaining, own2.Status)
	}
	if ob.asks[50].TotalVolume != volBefore {
		t.Errorf("TotalVolume: want %d (unchanged), got %d", volBefore, ob.asks[50].TotalVolume)
	}
}

// TestSTPSkipMidSweep: queue order is B, A (own, skipped), B.
// Incoming A Buy fills both B orders and skips the A in the middle.
func TestSTPSkipMidSweep(t *testing.T) {
	ob := NewOrderBook()

	b1 := restOrder(ob, "s_b1", "bob", Sell, 50, 3)
	ownMid := restOrder(ob, "s_a1", "alice", Sell, 50, 3)
	b2 := restOrder(ob, "s_b2", "bob", Sell, 50, 3)
	// Queue: s_b1 → s_a1 → s_b2, TotalVolume=9

	buy, _ := submitOrder(ob, "b1", "alice", Buy, 50, 6)

	// Both B orders filled.
	if b1.Status != Filled {
		t.Errorf("b1 Status: want Filled, got %v", b1.Status)
	}
	if b2.Status != Filled {
		t.Errorf("b2 Status: want Filled, got %v", b2.Status)
	}

	// Own order in the middle untouched.
	if ownMid.Remaining != 3 {
		t.Errorf("ownMid Remaining: want 3, got %d", ownMid.Remaining)
	}
	if ownMid.Status != Open {
		t.Errorf("ownMid Status: want Open, got %v", ownMid.Status)
	}

	// Own order must still be the sole occupant of asks[50].
	lvl := ob.asks[50]
	if lvl == nil {
		t.Fatal("asks[50] should still exist")
	}
	front := lvl.queue.Front().Value.(*Order)
	if front.ID != "s_a1" {
		t.Errorf("front of asks[50]: want s_a1, got %s", front.ID)
	}
	if lvl.queue.Len() != 1 {
		t.Errorf("queue length: want 1, got %d", lvl.queue.Len())
	}

	// TotalVolume: 9 - 3 (b1) - 3 (b2) = 3; the own skip does not subtract.
	if lvl.TotalVolume != 3 {
		t.Errorf("TotalVolume: want 3, got %d", lvl.TotalVolume)
	}

	// Incoming Buy filled 6 (3+3).
	if buy.Status != Filled {
		t.Errorf("buy Status: want Filled, got %v", buy.Status)
	}

	tape := ob.Tape()
	if len(tape) != 2 {
		t.Fatalf("tape length: want 2, got %d", len(tape))
	}
	if tape[0].MakerOrderID != "s_b1" || tape[1].MakerOrderID != "s_b2" {
		t.Errorf("trade order wrong: got %s, %s", tape[0].MakerOrderID, tape[1].MakerOrderID)
	}
}

// TestSTPSkipEntireLevelThenFillWorseLevel: the best ask level contains only
// owner A's own Sell. The incoming Buy skips the whole level and fills against
// owner B at a worse (higher) ask price within the limit.
func TestSTPSkipEntireLevelThenFillWorseLevel(t *testing.T) {
	ob := NewOrderBook()

	ownSell := restOrder(ob, "s_a1", "alice", Sell, 50, 5) // best ask, sole occupant
	otherSell := restOrder(ob, "s_b1", "bob", Sell, 52, 5) // worse ask

	buy, _ := submitOrder(ob, "b1", "alice", Buy, 55, 5)

	// Exactly one trade, at price 52 (the worse level that was actually filled).
	tape := ob.Tape()
	if len(tape) != 1 {
		t.Fatalf("tape length: want 1, got %d", len(tape))
	}
	if tape[0].Price != 52 {
		t.Errorf("trade price: want 52, got %d", tape[0].Price)
	}
	if tape[0].MakerOrderID != "s_b1" {
		t.Errorf("trade maker: want s_b1, got %s", tape[0].MakerOrderID)
	}

	// A's Sell at level 50 is completely untouched.
	if ownSell.Remaining != 5 {
		t.Errorf("ownSell Remaining: want 5, got %d", ownSell.Remaining)
	}
	if ownSell.Status != Open {
		t.Errorf("ownSell Status: want Open, got %v", ownSell.Status)
	}
	lvl50 := ob.asks[50]
	if lvl50 == nil {
		t.Fatal("asks[50] should still exist — nothing was filled there")
	}
	if lvl50.TotalVolume != 5 {
		t.Errorf("asks[50].TotalVolume: want 5 (unchanged), got %d", lvl50.TotalVolume)
	}

	// bestAsk still points to 50 because level 50 never emptied.
	if ob.bestAsk != 50 {
		t.Errorf("bestAsk: want 50, got %d", ob.bestAsk)
	}

	// B's Sell is fully consumed.
	if otherSell.Status != Filled {
		t.Errorf("otherSell Status: want Filled, got %v", otherSell.Status)
	}

	// Incoming Buy is fully filled.
	if buy.Status != Filled {
		t.Errorf("buy Status: want Filled, got %v", buy.Status)
	}
	if buy.Remaining != 0 {
		t.Errorf("buy Remaining: want 0, got %d", buy.Remaining)
	}
}

// TestSTPTotalVolumeUnchangedBySkip: asserts TotalVolume accounts for only
// the quantities actually filled; the skipped own quantity is never deducted.
func TestSTPTotalVolumeUnchangedBySkip(t *testing.T) {
	ob := NewOrderBook()

	restOrder(ob, "s_a1", "alice", Sell, 50, 10) // own — will be skipped
	restOrder(ob, "s_b1", "bob", Sell, 50, 5)    // other — will fill

	volBefore := ob.asks[50].TotalVolume // 15

	_, _ = submitOrder(ob, "b1", "alice", Buy, 50, 5)

	lvl := ob.asks[50]
	if lvl == nil {
		t.Fatal("asks[50] should still exist")
	}

	filledQty := int64(5)
	wantVol := volBefore - filledQty // 15 - 5 = 10
	if lvl.TotalVolume != wantVol {
		t.Errorf("TotalVolume: want %d (before=%d minus filled=%d), got %d",
			wantVol, volBefore, filledQty, lvl.TotalVolume)
	}

	// Only own order remains; its Remaining is untouched.
	if lvl.queue.Len() != 1 {
		t.Errorf("queue length: want 1 (own order only), got %d", lvl.queue.Len())
	}
	remaining := lvl.queue.Front().Value.(*Order)
	if remaining.ID != "s_a1" || remaining.Remaining != 10 {
		t.Errorf("remaining order: want s_a1/Remaining=10, got %s/%d",
			remaining.ID, remaining.Remaining)
	}
}
