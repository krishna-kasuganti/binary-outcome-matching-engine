package engine

import "errors"

var (
	ErrNotFound      = errors.New("order not found")
	ErrAlreadyFilled = errors.New("order already filled")
)

// Rest places an order that does not cross into the book.
func (ob *OrderBook) Rest(o *Order) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	ob.seq++
	o.Sequence = ob.seq
	o.Status = Open
	ob.restLocked(o)
}

// restLocked inserts o into its price level. Caller must hold the exclusive lock.
// It does not stamp the sequence or set the status.
func (ob *OrderBook) restLocked(o *Order) {
	idx := int(o.Price)
	if o.Side == Buy {
		if ob.bids[idx] == nil {
			ob.bids[idx] = newPriceLevel(o.Price)
		}
		lvl := ob.bids[idx]
		o.elem = lvl.queue.PushBack(o)
		lvl.TotalVolume += o.Remaining
		ob.lookup[o.ID] = o.elem
		if idx > ob.bestBid {
			ob.bestBid = idx
		}
	} else {
		if ob.asks[idx] == nil {
			ob.asks[idx] = newPriceLevel(o.Price)
		}
		lvl := ob.asks[idx]
		o.elem = lvl.queue.PushBack(o)
		lvl.TotalVolume += o.Remaining
		ob.lookup[o.ID] = o.elem
		if ob.bestAsk == 0 || idx < ob.bestAsk {
			ob.bestAsk = idx
		}
	}
}

// Cancel removes a resting order by ID.
func (ob *OrderBook) Cancel(id string) error {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	elem, ok := ob.lookup[id]
	if !ok {
		return ErrNotFound
	}

	o := elem.Value.(*Order)
	if o.Status == Filled {
		return ErrAlreadyFilled
	}

	idx := int(o.Price)
	var lvl *PriceLevel
	if o.Side == Buy {
		lvl = ob.bids[idx]
	} else {
		lvl = ob.asks[idx]
	}

	lvl.queue.Remove(elem)
	lvl.TotalVolume -= o.Remaining
	delete(ob.lookup, id)
	o.Status = Cancelled

	if lvl.queue.Len() == 0 {
		if o.Side == Buy {
			ob.bids[idx] = nil
			if ob.bestBid == idx {
				ob.bestBid = ob.nextBestBid(idx)
			}
		} else {
			ob.asks[idx] = nil
			if ob.bestAsk == idx {
				ob.bestAsk = ob.nextBestAsk(idx)
			}
		}
	}

	return nil
}

func (ob *OrderBook) nextBestBid(from int) int {
	for i := from - 1; i >= 1; i-- {
		if ob.bids[i] != nil && ob.bids[i].queue.Len() > 0 {
			return i
		}
	}
	return 0
}

func (ob *OrderBook) nextBestAsk(from int) int {
	for i := from + 1; i <= 99; i++ {
		if ob.asks[i] != nil && ob.asks[i].queue.Len() > 0 {
			return i
		}
	}
	return 0
}
