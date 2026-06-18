package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"

	"matching-engine/engine"
)

// NewServer wires all routes onto a new ServeMux and returns it as an http.Handler.
func NewServer(ob *engine.OrderBook) http.Handler {
	s := &server{ob: ob}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders", s.handleSubmit)
	mux.HandleFunc("DELETE /orders/{id}", s.handleCancel)
	mux.HandleFunc("GET /orders/{id}", s.handleGetOrder)
	mux.HandleFunc("GET /orderbook", s.handleSnapshot)
	mux.HandleFunc("GET /trades", s.handleTrades)
	return mux
}

type server struct {
	ob *engine.OrderBook
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newOrderID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func statusString(s engine.Status) string {
	switch s {
	case engine.Open:
		return "open"
	case engine.PartiallyFilled:
		return "partially_filled"
	case engine.Filled:
		return "filled"
	case engine.Cancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// ── POST /orders ──────────────────────────────────────────────────────────────

type submitReq struct {
	OwnerID  string `json:"owner_id"`
	Side     string `json:"side"`
	Price    int64  `json:"price"`
	Quantity int64  `json:"quantity"`
}

type fillResp struct {
	Price        int64  `json:"price"`
	Quantity     int64  `json:"quantity"`
	MakerOrderID string `json:"maker_order_id"`
	TakerOrderID string `json:"taker_order_id"`
}

type submitResp struct {
	OrderID   string     `json:"order_id"`
	Status    string     `json:"status"`
	Remaining int64      `json:"remaining"`
	Fills     []fillResp `json:"fills"`
}

func (s *server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var req submitReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return
	}
	if req.OwnerID == "" {
		writeError(w, http.StatusBadRequest, "owner_id is required")
		return
	}
	var side engine.Side
	switch req.Side {
	case "buy":
		side = engine.Buy
	case "sell":
		side = engine.Sell
	default:
		writeError(w, http.StatusBadRequest, `side must be "buy" or "sell"`)
		return
	}
	if req.Price < 1 || req.Price > 99 {
		writeError(w, http.StatusBadRequest, "price outside valid range [1, 99]")
		return
	}
	if req.Quantity <= 0 {
		writeError(w, http.StatusBadRequest, "quantity must be greater than zero")
		return
	}

	o := &engine.Order{
		ID:        newOrderID(),
		OwnerID:   req.OwnerID,
		Side:      side,
		Price:     req.Price,
		Quantity:  req.Quantity,
		Remaining: req.Quantity,
	}

	trades, err := s.ob.Submit(o)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fills := make([]fillResp, len(trades))
	for i, t := range trades {
		fills[i] = fillResp{
			Price:        t.Price,
			Quantity:     t.Quantity,
			MakerOrderID: t.MakerOrderID,
			TakerOrderID: t.TakerOrderID,
		}
	}

	writeJSON(w, http.StatusOK, submitResp{
		OrderID:   o.ID,
		Status:    statusString(o.Status),
		Remaining: o.Remaining,
		Fills:     fills,
	})
}

// ── DELETE /orders/{id} ──────────────────────────────────────────────────────

func (s *server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	switch err := s.ob.Cancel(id); err {
	case nil:
		writeJSON(w, http.StatusOK, map[string]string{"message": "cancelled"})
	case engine.ErrNotFound:
		writeError(w, http.StatusNotFound, "order not found")
	case engine.ErrAlreadyFilled:
		writeError(w, http.StatusConflict, "order already filled")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// ── GET /orders/{id} ─────────────────────────────────────────────────────────

type orderResp struct {
	Quantity  int64  `json:"quantity"`
	Remaining int64  `json:"remaining"`
	Status    string `json:"status"`
}

func (s *server) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, ok := s.ob.GetOrder(id)
	if !ok {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}
	writeJSON(w, http.StatusOK, orderResp{
		Quantity:  info.Quantity,
		Remaining: info.Remaining,
		Status:    statusString(info.Status),
	})
}

// ── GET /orderbook ────────────────────────────────────────────────────────────

type levelResp struct {
	Price       int64 `json:"price"`
	TotalVolume int64 `json:"total_volume"`
}

type bookResp struct {
	BestBid int64       `json:"best_bid"`
	BestAsk int64       `json:"best_ask"`
	Bids    []levelResp `json:"bids"`
	Asks    []levelResp `json:"asks"`
}

func (s *server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	depth := 0
	if d := r.URL.Query().Get("depth"); d != "" {
		v, err := strconv.Atoi(d)
		if err != nil || v < 1 {
			writeError(w, http.StatusBadRequest, "depth must be a positive integer")
			return
		}
		depth = v
	}

	snap := s.ob.Snapshot(depth)

	bids := make([]levelResp, len(snap.Bids))
	for i, l := range snap.Bids {
		bids[i] = levelResp{Price: l.Price, TotalVolume: l.TotalVolume}
	}
	asks := make([]levelResp, len(snap.Asks))
	for i, l := range snap.Asks {
		asks[i] = levelResp{Price: l.Price, TotalVolume: l.TotalVolume}
	}

	writeJSON(w, http.StatusOK, bookResp{
		BestBid: int64(snap.BestBid),
		BestAsk: int64(snap.BestAsk),
		Bids:    bids,
		Asks:    asks,
	})
}

// ── GET /trades ───────────────────────────────────────────────────────────────

type tradeResp struct {
	Sequence     int64  `json:"sequence"`
	Price        int64  `json:"price"`
	Quantity     int64  `json:"quantity"`
	MakerOrderID string `json:"maker_order_id"`
	TakerOrderID string `json:"taker_order_id"`
	MakerOwner   string `json:"maker_owner"`
	TakerOwner   string `json:"taker_owner"`
}

func (s *server) handleTrades(w http.ResponseWriter, r *http.Request) {
	tape := s.ob.Tape()
	resp := make([]tradeResp, len(tape))
	for i, t := range tape {
		resp[i] = tradeResp{
			Sequence:     t.Sequence,
			Price:        t.Price,
			Quantity:     t.Quantity,
			MakerOrderID: t.MakerOrderID,
			TakerOrderID: t.TakerOrderID,
			MakerOwner:   t.MakerOwner,
			TakerOwner:   t.TakerOwner,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
