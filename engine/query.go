package engine

// OrderInfo is a snapshot of an order's key fields for external callers.
type OrderInfo struct {
	Quantity  int64
	Remaining int64
	Status    Status
}

// GetOrder returns a snapshot of the named order under RLock.
// Returns (info, true) if the ID is in the lookup map, (zero, false) otherwise.
func (ob *OrderBook) GetOrder(id string) (OrderInfo, bool) {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	elem, ok := ob.lookup[id]
	if !ok {
		return OrderInfo{}, false
	}
	o := elem.Value.(*Order)
	return OrderInfo{Quantity: o.Quantity, Remaining: o.Remaining, Status: o.Status}, true
}

// LevelInfo summarises a single price level.
type LevelInfo struct {
	Price       int64
	TotalVolume int64
}

// BookSnapshot is a consistent point-in-time view of both sides of the book.
type BookSnapshot struct {
	Bids    []LevelInfo
	Asks    []LevelInfo
	BestBid int
	BestAsk int
}

// Snapshot returns a BookSnapshot under RLock.
// Bids are sorted high-to-low; asks low-to-high.
// If depth > 0, each side is capped at that many levels from the top.
func (ob *OrderBook) Snapshot(depth int) BookSnapshot {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	snap := BookSnapshot{
		BestBid: ob.bestBid,
		BestAsk: ob.bestAsk,
		Bids:    []LevelInfo{},
		Asks:    []LevelInfo{},
	}

	for i := 99; i >= 1; i-- {
		if ob.bids[i] != nil && ob.bids[i].TotalVolume > 0 {
			snap.Bids = append(snap.Bids, LevelInfo{Price: int64(i), TotalVolume: ob.bids[i].TotalVolume})
			if depth > 0 && len(snap.Bids) >= depth {
				break
			}
		}
	}

	for i := 1; i <= 99; i++ {
		if ob.asks[i] != nil && ob.asks[i].TotalVolume > 0 {
			snap.Asks = append(snap.Asks, LevelInfo{Price: int64(i), TotalVolume: ob.asks[i].TotalVolume})
			if depth > 0 && len(snap.Asks) >= depth {
				break
			}
		}
	}

	return snap
}
