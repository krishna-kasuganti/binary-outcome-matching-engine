# Project Brief: Binary Outcome Matching Engine (Go)

This project is a high-performance, thread-safe financial order-matching engine built natively in Go.

## 1. Development & Verification Commands
- Build Project: `go build -o matching-engine main.go`
- Run Main App: `go run main.go`
- Run All Tests: `go test -v ./...`
- Concurrency Race Check: `go test -v -race ./...`

## 2. Core Architectural Guardrails
- CRITICAL: Never use floating-point numbers (`float32`, `float64`) for contract prices or values.
- All pricing logic must use `int64` representing cents. Validate that prices stay strictly within the binary outcome range (1-99).
- Data Structures: Use an aggregated Price-Level approach (Ordered pricing keys holding FIFO execution queues). Avoid flat slices that require shifting elements on insert.
- Concurrency: Prevent cancel-while-matching race conditions using a `sync.RWMutex`. Use exclusive locks (`Lock()`) for state writes (orders/cancels) and shared locks (`RLock()`) for aggregating snapshots.
- Multi-User Isolation: Implement a lookup map (`map[string]*Order`) to manage instant $O(1)$ cancellations.

## 3. Workflow & Session Logging
- Keep responses highly concise and direct. Skip conversational filler.
- Document every major feature addition or bug fix into `ai_logs/claude_session.md`.