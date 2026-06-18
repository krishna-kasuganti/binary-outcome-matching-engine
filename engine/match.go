package engine

// Submit validates, matches, and rests any unfilled remainder.
// It returns a copy of the trades that executed during this call.
func (ob *OrderBook) Submit(o *Order) ([]Trade, error) {
	if err := ValidateOrder(o); err != nil {
		return nil, err
	}

	ob.mu.Lock()
	defer ob.mu.Unlock()

	ob.seq++
	o.Sequence = ob.seq
	tapeBefore := len(ob.tape)

	if o.Side == Buy {
		for cur := ob.bestAsk; cur != 0 && cur <= int(o.Price) && o.Remaining > 0; {
			lvl := ob.asks[cur]
			ob.matchLevel(o, lvl)
			nextCur := ob.nextBestAsk(cur)
			if lvl.queue.Len() == 0 {
				ob.asks[cur] = nil
				if ob.bestAsk == cur {
					ob.bestAsk = nextCur
				}
			}
			cur = nextCur
		}
	} else {
		for cur := ob.bestBid; cur != 0 && cur >= int(o.Price) && o.Remaining > 0; {
			lvl := ob.bids[cur]
			ob.matchLevel(o, lvl)
			nextCur := ob.nextBestBid(cur)
			if lvl.queue.Len() == 0 {
				ob.bids[cur] = nil
				if ob.bestBid == cur {
					ob.bestBid = nextCur
				}
			}
			cur = nextCur
		}
	}

	switch {
	case o.Remaining == 0:
		o.Status = Filled
	case o.Remaining < o.Quantity:
		o.Status = PartiallyFilled
		ob.restLocked(o)
	default:
		o.Status = Open
		ob.restLocked(o)
	}

	fills := make([]Trade, len(ob.tape)-tapeBefore)
	copy(fills, ob.tape[tapeBefore:])
	return fills, nil
}

// matchLevel walks the FIFO queue of lvl and fills against incoming until
// the level is exhausted or incoming.Remaining reaches zero.
// Same-owner orders are skipped without modification (STP skip-the-cross).
// Caller must hold the exclusive lock.
func (ob *OrderBook) matchLevel(incoming *Order, lvl *PriceLevel) {
	for e := lvl.queue.Front(); e != nil && incoming.Remaining > 0; {
		resting := e.Value.(*Order)

		if resting.OwnerID == incoming.OwnerID {
			e = e.Next()
			continue
		}

		fillQty := min(incoming.Remaining, resting.Remaining)

		incoming.Remaining -= fillQty
		resting.Remaining -= fillQty
		lvl.TotalVolume -= fillQty

		ob.tradeSeq++
		ob.tape = append(ob.tape, Trade{
			Sequence:     ob.tradeSeq,
			Price:        resting.Price,
			Quantity:     fillQty,
			MakerOrderID: resting.ID,
			TakerOrderID: incoming.ID,
			MakerOwner:   resting.OwnerID,
			TakerOwner:   incoming.OwnerID,
		})

		next := e.Next()
		if resting.Remaining == 0 {
			resting.Status = Filled
			lvl.queue.Remove(e)
			delete(ob.lookup, resting.ID)
		} else {
			resting.Status = PartiallyFilled
		}
		e = next
	}
}

// Tape returns a snapshot copy of all trades executed so far.
func (ob *OrderBook) Tape() []Trade {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	out := make([]Trade, len(ob.tape))
	copy(out, ob.tape)
	return out
}
