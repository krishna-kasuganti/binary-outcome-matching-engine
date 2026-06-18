package engine

import (
	"fmt"
	"sync"
	"testing"
)

// checkBookInvariants verifies three structural invariants on ob after all
// concurrent operations have completed (called after wg.Wait).
//
//  1. No crossed book: bestBid < bestAsk when both are non-zero.
//  2. Each price level's TotalVolume equals the sum of Remaining across its
//     resting orders.
//  3. The tape total (sum of trade quantities) equals the sum of
//     (Quantity − Remaining) across all buy orders in allOrders.
//
// The function must be called with the full set of orders ever submitted to ob
// so that the tape reconciliation covers all filled quantities.
func checkBookInvariants(t *testing.T, ob *OrderBook, allOrders []*Order) {
	t.Helper()
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	// 1. No crossed book.
	if ob.bestBid != 0 && ob.bestAsk != 0 && ob.bestBid >= ob.bestAsk {
		t.Errorf("crossed book: bestBid=%d >= bestAsk=%d", ob.bestBid, ob.bestAsk)
	}

	// 2. TotalVolume == sum of Remaining for each price level.
	for i := 1; i <= 99; i++ {
		if lvl := ob.bids[i]; lvl != nil {
			var sum int64
			for e := lvl.queue.Front(); e != nil; e = e.Next() {
				sum += e.Value.(*Order).Remaining
			}
			if lvl.TotalVolume != sum {
				t.Errorf("bids[%d]: TotalVolume=%d != sumRemaining=%d",
					i, lvl.TotalVolume, sum)
			}
		}
		if lvl := ob.asks[i]; lvl != nil {
			var sum int64
			for e := lvl.queue.Front(); e != nil; e = e.Next() {
				sum += e.Value.(*Order).Remaining
			}
			if lvl.TotalVolume != sum {
				t.Errorf("asks[%d]: TotalVolume=%d != sumRemaining=%d",
					i, lvl.TotalVolume, sum)
			}
		}
	}

	// 3. Tape total == buy-side filled quantity.
	// Each trade of Q units fills Q from exactly one buy and Q from exactly
	// one sell, so sum(tape quantities) always equals sum(Qty-Rem) for buys.
	var tapeFilled int64
	for _, tr := range ob.tape {
		tapeFilled += tr.Quantity
	}
	var buyFilled int64
	for _, o := range allOrders {
		if o.Side == Buy {
			buyFilled += o.Quantity - o.Remaining
		}
	}
	if tapeFilled != buyFilled {
		t.Errorf("tape total=%d != buy orders filled=%d", tapeFilled, buyFilled)
	}
}

// ── Test 1: concurrent submits ────────────────────────────────────────────────

// TestConcurrentSubmits launches buyers at prices 44–53 and sellers at 47–56
// concurrently (overlapping at 47–53) and verifies all structural invariants
// once every goroutine has returned.
func TestConcurrentSubmits(t *testing.T) {
	const numBuyers = 20
	const numSellers = 20
	const each = 10

	ob := NewOrderBook()
	buyOrders := make([][]*Order, numBuyers)
	sellOrders := make([][]*Order, numSellers)

	var wg sync.WaitGroup

	for g := 0; g < numBuyers; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			local := make([]*Order, each)
			for i := 0; i < each; i++ {
				o := &Order{
					ID:        fmt.Sprintf("buy-%d-%d", gid, i),
					OwnerID:   fmt.Sprintf("buyer-%d", gid),
					Side:      Buy,
					Price:     int64(44 + (gid+i)%10), // 44–53
					Quantity:  5,
					Remaining: 5,
				}
				ob.Submit(o)
				local[i] = o
			}
			buyOrders[gid] = local
		}(g)
	}

	for g := 0; g < numSellers; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			local := make([]*Order, each)
			for i := 0; i < each; i++ {
				o := &Order{
					ID:        fmt.Sprintf("sell-%d-%d", gid, i),
					OwnerID:   fmt.Sprintf("seller-%d", gid),
					Side:      Sell,
					Price:     int64(47 + (gid+i)%10), // 47–56
					Quantity:  5,
					Remaining: 5,
				}
				ob.Submit(o)
				local[i] = o
			}
			sellOrders[gid] = local
		}(g)
	}

	wg.Wait()

	all := make([]*Order, 0, (numBuyers+numSellers)*each)
	for _, lo := range buyOrders {
		all = append(all, lo...)
	}
	for _, lo := range sellOrders {
		all = append(all, lo...)
	}
	checkBookInvariants(t, ob, all)
}

// ── Test 2: concurrent submits and cancels ────────────────────────────────────

// TestConcurrentSubmitsAndCancels pre-populates the book with 80 resting bids
// (prices 45–54) then races 20 aggressor-sell goroutines against 80 cancel
// goroutines, all launched simultaneously. After all complete it verifies:
//   - Every cancel result is nil (cancelled before match) or ErrNotFound
//     (order was consumed by a match before the cancel goroutine ran).
//   - All three structural invariants hold on the final book.
func TestConcurrentSubmitsAndCancels(t *testing.T) {
	const numResting = 80
	const numAggressors = 20
	const each = 5

	ob := NewOrderBook()

	resting := make([]*Order, numResting)
	for i := 0; i < numResting; i++ {
		o := &Order{
			ID:        fmt.Sprintf("rest-%d", i),
			OwnerID:   fmt.Sprintf("maker-%d", i),
			Side:      Buy,
			Price:     int64(45 + i%10), // 45–54
			Quantity:  5,
			Remaining: 5,
		}
		ob.Rest(o)
		resting[i] = o
	}

	aggOrders := make([][]*Order, numAggressors)
	cancelErrs := make([]error, numResting) // each index written by exactly one goroutine

	var wg sync.WaitGroup

	// Sell aggressors at prices 44–53: all cross the resting bids at 45–54.
	for g := 0; g < numAggressors; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			local := make([]*Order, each)
			for i := 0; i < each; i++ {
				o := &Order{
					ID:        fmt.Sprintf("agg-%d-%d", gid, i),
					OwnerID:   fmt.Sprintf("aggressor-%d", gid),
					Side:      Sell,
					Price:     int64(44 + (gid+i)%10), // 44–53
					Quantity:  10,
					Remaining: 10,
				}
				ob.Submit(o)
				local[i] = o
			}
			aggOrders[gid] = local
		}(g)
	}

	// One cancel goroutine per resting order (n is unique per goroutine).
	for i := 0; i < numResting; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			cancelErrs[n] = ob.Cancel(fmt.Sprintf("rest-%d", n))
		}(i)
	}

	wg.Wait()

	for i, err := range cancelErrs {
		if err != nil && err != ErrNotFound {
			t.Errorf("cancel rest-%d: unexpected error %v", i, err)
		}
	}

	all := make([]*Order, 0, numResting+numAggressors*each)
	all = append(all, resting...)
	for _, lo := range aggOrders {
		all = append(all, lo...)
	}
	checkBookInvariants(t, ob, all)
}

// ── Test 3: cancel while matching ─────────────────────────────────────────────

// TestCancelWhileMatching is the canonical cancel-while-matching race.
// Each iteration:
//  1. Rests a single maker Sell@50 qty=10.
//  2. Races a Submit (taker Buy@50 qty=10) against a Cancel of that same maker.
//  3. Asserts exactly one of two legal outcomes occurred:
//     - Match won: tape has one trade qty=10; cancel returned ErrNotFound or
//     ErrAlreadyFilled; maker.Status==Filled.
//     - Cancel won: tape is empty; cancel returned nil; maker.Status==Cancelled;
//     taker.Status==Open.
//  4. Calls checkBookInvariants to confirm book structure is clean after each run.
//
// A "start gun" channel (closed after both goroutines are created) maximises
// lock contention between the Submit and Cancel paths.
func TestCancelWhileMatching(t *testing.T) {
	const runs = 500

	for run := 0; run < runs; run++ {
		ob := NewOrderBook()

		maker := &Order{
			ID:        "maker",
			OwnerID:   "alice",
			Side:      Sell,
			Price:     50,
			Quantity:  10,
			Remaining: 10,
		}
		ob.Rest(maker)

		taker := &Order{
			ID:        "taker",
			OwnerID:   "bob",
			Side:      Buy,
			Price:     50,
			Quantity:  10,
			Remaining: 10,
		}

		var fills []Trade
		var cancelErr error

		ready := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-ready
			fills, _ = ob.Submit(taker)
		}()

		go func() {
			defer wg.Done()
			<-ready
			cancelErr = ob.Cancel("maker")
		}()

		close(ready)
		wg.Wait()

		switch len(fills) {
		case 1:
			// Match won: full fill; cancel must have observed the order gone.
			if fills[0].Quantity != 10 {
				t.Fatalf("run %d: expected full fill qty=10, got %d",
					run, fills[0].Quantity)
			}
			if cancelErr != ErrNotFound && cancelErr != ErrAlreadyFilled {
				t.Fatalf("run %d: match won, unexpected cancel error: %v",
					run, cancelErr)
			}
			if maker.Status != Filled {
				t.Fatalf("run %d: maker status: want Filled, got %v",
					run, maker.Status)
			}
			tape := ob.Tape()
			if len(tape) != 1 || tape[0].Quantity != 10 {
				t.Fatalf("run %d: tape mismatch after match win: %v", run, tape)
			}
		case 0:
			// Cancel won: no trades; taker rests Open.
			if cancelErr != nil {
				t.Fatalf("run %d: cancel won but error: %v", run, cancelErr)
			}
			if maker.Status != Cancelled {
				t.Fatalf("run %d: maker status: want Cancelled, got %v",
					run, maker.Status)
			}
			if taker.Status != Open {
				t.Fatalf("run %d: taker status: want Open, got %v",
					run, taker.Status)
			}
			tape := ob.Tape()
			if len(tape) != 0 {
				t.Fatalf("run %d: expected empty tape after cancel win, got %d trade(s)",
					run, len(tape))
			}
		default:
			t.Fatalf("run %d: illegal outcome — %d fills (double fill?)", run, len(fills))
		}

		checkBookInvariants(t, ob, []*Order{maker, taker})
	}
}
