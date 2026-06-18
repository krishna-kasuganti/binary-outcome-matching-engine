# Project Brief: Binary Outcome Matching Engine (Go)

This project is a thread-safe financial order-matching engine built natively in Go for a binary outcome market with prices bounded strictly between 1 and 99.

## 1. Development & Verification Commands
- Build Project: `go build -o matching-engine main.go`
- Run Main App: `go run main.go`
- Run All Tests: `go test -v ./...`
- Concurrency Race Check: `go test -v -race ./...`

## 2. Core Architectural Guardrails
- CRITICAL: Never use floating-point numbers (`float32`, `float64`) for contract prices or values.
- All pricing logic must use `int64` representing cents. Validate that prices stay strictly within the range 1 to 99 inclusive.
- Order book: use a fixed `[100]*PriceLevel` array indexed directly by price (index 0 unused), with integer bestBid and bestAsk cursors tracking the live top of book. Each PriceLevel holds a `container/list` FIFO queue ordered by arrival.
- Time priority: stamp each accepted order with a monotonic int64 sequence number from a single atomic counter on the OrderBook. Order each FIFO queue by sequence. Never use wall clock timestamps for priority.
- O(1) cancellation: maintain a `map[string]*list.Element` keyed by order ID so cancel unlinks the queue node directly without scanning the level.
- Maker price execution: trades print at the resting (maker) order's price, never the incoming order's price.
- Self trade policy: when a resting order's owner equals the incoming order's owner, skip the cross, leave the resting order untouched, and continue matching the aggressor against other accounts.
- Concurrency: protect the book with a `sync.RWMutex`. The entire match loop runs under an exclusive `Lock()`, never an `RLock()` that upgrades mid match. Use `RLock()` only for the read-only order book snapshot.

## 3. Workflow & Session Logging
- Keep responses highly concise and direct. Skip conversational filler.
- Document every major feature addition or bug fix into `ai_logs/claude_session.md`.