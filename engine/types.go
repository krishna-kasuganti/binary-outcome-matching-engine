package engine

import (
	"container/list"
	"fmt"
	"sync"
)

type Side int8

const (
	Buy  Side = 1
	Sell Side = 2
)

type Status int8

const (
	Open            Status = 1
	PartiallyFilled Status = 2
	Filled          Status = 3
	Cancelled       Status = 4
)

type Order struct {
	ID        string
	OwnerID   string
	Side      Side
	Price     int64
	Quantity  int64
	Remaining int64
	Sequence  int64
	Status    Status
	elem      *list.Element
}

type PriceLevel struct {
	Price       int64
	queue       *list.List
	TotalVolume int64
}

func newPriceLevel(price int64) *PriceLevel {
	return &PriceLevel{
		Price: price,
		queue: list.New(),
	}
}

type OrderBook struct {
	bids     [100]*PriceLevel
	asks     [100]*PriceLevel
	bestBid  int
	bestAsk  int
	lookup   map[string]*list.Element
	seq      int64
	tape     []Trade
	tradeSeq int64
	mu       sync.RWMutex
}

func NewOrderBook() *OrderBook {
	return &OrderBook{
		lookup: make(map[string]*list.Element),
	}
}

type Trade struct {
	Sequence     int64
	Price        int64
	Quantity     int64
	MakerOrderID string
	TakerOrderID string
	MakerOwner   string
	TakerOwner   string
}

func ValidateOrder(o *Order) error {
	if o.OwnerID == "" {
		return fmt.Errorf("ownerID is required")
	}
	if o.Price < 1 || o.Price > 99 {
		return fmt.Errorf("price %d is outside valid range [1, 99]", o.Price)
	}
	if o.Quantity <= 0 {
		return fmt.Errorf("quantity %d must be greater than zero", o.Quantity)
	}
	return nil
}
