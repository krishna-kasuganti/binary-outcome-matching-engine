# Claude Session Log

## Stage 1 — Core Types & Validation (2026-06-18)

**Files created:** `engine/types.go`, `engine/types_test.go`

**What was built:**

- `Side` typed enum (`Buy`, `Sell`) and `Status` typed enum (`Open`, `PartiallyFilled`, `Filled`, `Cancelled`) — both `int8` to keep the struct small.
- `Order` struct with all required fields: `ID`, `OwnerID`, `Side`, `Price`, `Quantity` (original size, never mutated), `Remaining` (unfilled size), `Sequence`, `Status`, and a private `elem *list.Element` back-reference enabling O(1) cancel from any price level queue.
- `PriceLevel` struct with `Price int64`, a private `*list.List` FIFO queue, and `TotalVolume int64` maintained incrementally.
- `OrderBook` struct with `[100]*PriceLevel` arrays for bids and asks (index 0 unused, direct price indexing), `bestBid`/`bestAsk` int cursors, `map[string]*list.Element` lookup for O(1) cancel, `int64` sequence counter, and `sync.RWMutex`. `NewOrderBook()` constructor returns a ready instance.
- `Trade` struct with sequence, price, quantity, maker/taker order IDs, and maker/taker owner IDs.
- `ValidateOrder(*Order) error` — rejects price outside [1, 99], quantity ≤ 0, or empty `OwnerID`, with a distinct error message per case.

**Tests:** 8 table-driven cases covering all rejection and valid-boundary scenarios. All pass (`go test -v ./engine/...`).

## Stage 2 — Resting Orders & Cancellation (2026-06-18)

**Files created:** `engine/book.go`, `engine/book_test.go`

**What was built:**

- `OrderBook.Rest(*Order)` — takes exclusive `Lock()`, stamps sequence, inserts order at back of its price-level FIFO queue (creating the `PriceLevel` slot if nil), increments `TotalVolume`, stores `*list.Element` in lookup map, updates `bestBid`/`bestAsk` cursors, sets `Status = Open`.
- `OrderBook.Cancel(id string) error` — takes exclusive `Lock()`, returns `ErrNotFound` if ID absent, returns `ErrAlreadyFilled` if status is `Filled`, otherwise unlinks the element from its queue in O(1), decrements `TotalVolume`, deletes from lookup, sets `Status = Cancelled`. If the level empties, nils the slot and walks the cursor to the next non-empty level via `nextBestBid`/`nextBestAsk`.
- `ErrNotFound` and `ErrAlreadyFilled` sentinel errors.
- `nextBestBid(from int) int` / `nextBestAsk(from int) int` — internal helpers that scan for the next non-empty level after a cancellation empties the current best.

**Tests (10 cases, all pass under `go test -v -race ./...`):**
- `TestRestSingleBid` / `TestRestSingleAsk` — single order lands at correct level, correct TotalVolume, correct best cursor, status Open, sequence stamped.
- `TestRestTwoOrdersSamePrice` — FIFO order verified by sequence and queue position; TotalVolume summed correctly.
- `TestBestCursorsMultiplePrices` — bestBid tracks highest bid, bestAsk tracks lowest ask across multiple price levels.
- `TestCancelReducesVolume` — TotalVolume decrements, order removed from lookup, status Cancelled, sibling order intact.
- `TestCancelLastOrderRecalculatesBestBid` / `TestCancelLastOrderRecalculatesBestAsk` — cursor walks to next non-empty level when the current best empties; nil slot confirmed.
- `TestCancelUnknownID` — returns `ErrNotFound`.
- `TestCancelFilledOrder` — returns `ErrAlreadyFilled`.
- `TestLookupStoresListElement` — asserts lookup value is `*list.Element`, not `*Order`.

## Stage 3 — Match Loop, Partial Fills, Trade Tape (2026-06-18)

**Files changed/created:** `engine/types.go` (extended), `engine/book.go` (refactored), `engine/match.go` (new), `engine/match_test.go` (new)

**What was built:**

- `tape []Trade` and `tradeSeq int64` added to `OrderBook`.
- `Tape() []Trade` — RLock accessor returning a snapshot copy of the trade tape.
- `restLocked(*Order)` — private helper extracted from `Rest`; assumes exclusive lock held, does not stamp sequence or set status. `Rest` now calls it after stamping sequence and setting `Open`.
- `Submit(*Order) error` — validates before acquiring the lock, then: stamps sequence, runs the match loop, calls `restLocked` for any remainder (status `Open` or `PartiallyFilled`), or marks `Filled` if fully consumed.
- `matchLevel(*Order, *PriceLevel)` — walks the level FIFO front-to-back under the existing exclusive lock; computes `fillQty = min(incoming.Remaining, resting.Remaining)`, decrements both `Remaining` and the level's `TotalVolume` atomically, appends a `Trade` at the **resting** price (maker price), captures `e.Next()` before any `Remove`, removes fully-filled resting orders from the queue and lookup map, advances the bestAsk/bestBid cursor when a level empties.

**Key invariants maintained:**
- `TotalVolume` always equals the sum of `Remaining` across all orders in a level — decremented by `fillQty` on every fill and by `Remaining` on cancel.
- Trade always prints at the maker (resting) price.
- FIFO within a price level is preserved; the `list.Element` back-reference enables O(1) removal.

**Tests (8 new cases, all pass under `go test -v -race -count=1 ./...`):**
- `TestFullMatch` — equal-size cross, both Filled, one trade at resting price.
- `TestAggressorPartial` — large Buy against small Sell; Sell Filled, Buy rests remainder with correct TotalVolume and lookup entry.
- `TestRestingPartial` — small Buy against large Sell; Buy Filled, Sell PartiallyFilled at queue front with decremented Remaining and TotalVolume.
- `TestNoCross` — Buy below bestAsk rests with zero trades, cursors unchanged.
- `TestMultiLevelSweep` — Buy clears three ask levels; trades in ascending price order, all levels nil'd, bestAsk=0.
- `TestTimePriority` — two Sells at same price; earlier sequence fills first.
- `TestMakerPrice` — Buy at 60 hits resting Sell at 55; trade prints at 55.
- `TestDriftCheck` — rest Sell 100, partial-fill 40, cancel remainder; TotalVolume reaches exactly 0, bestAsk recalculates to 0.

## Stage 4 — Self Trade Prevention (skip-the-cross) (2026-06-18)

**Files changed:** `engine/match.go`, `engine/stp_test.go` (new)

**What was built:**

- STP check in `matchLevel` ([match.go:65-68](engine/match.go#L65-L68)): before computing `fillQty`, compare `resting.OwnerID == incoming.OwnerID`. If equal, advance `e = e.Next()` and `continue` — no fill, no status change, no `TotalVolume` change, no queue removal.
- **Outer loop fix** (required by STP): the previous `Submit` loop used `ob.bestAsk` as both the sweep iterator and the book cursor, which infinite-loops when a level has only same-owner orders (the level never empties, so the cursor never advances). Fixed by using a local `cur` variable initialized to `ob.bestAsk`. After each level, `nextCur = ob.nextBestAsk(cur)` scans for the next non-nil level from `cur+1`. `ob.bestAsk` only updates when the level actually empties; `cur` always advances. Applied symmetrically to the Sell/bid side.

**Key invariants:**
- A skipped order's `Remaining`, `Status`, and `TotalVolume` are never touched.
- `bestAsk`/`bestBid` cursor does NOT change from a skip (level still occupies its slot).
- After exhausting all matchable levels, the incoming remainder rests as normal.

**Tests (4 new cases, all pass under `go test -v -race -count=1 ./...`):**
- `TestSTPSkipAndContinue` — own Sell at front skipped, B Sell behind it fills; own Sell untouched, TotalVolume drops only by filled qty.
- `TestSTPOnlyOwnLiquidity` — all opposing orders are own; zero trades, incoming rests at full Remaining, TotalVolume unchanged.
- `TestSTPSkipMidSweep` — queue is B→A(own)→B; incoming A fills both B orders, skips A in middle; A stays at queue front with Remaining=3, TotalVolume=9-3-3=3.
- `TestSTPTotalVolumeUnchangedBySkip` — explicit before/after assertion: TotalVolume = before − filledQty only, not − skippedQty.
- `TestSTPSkipEntireLevelThenFillWorseLevel` (added post-review) — A's Sell is the sole occupant of bestAsk=50; incoming A Buy skips the entire level, advances to asks[52] (B's Sell), fills there at price 52; asserts asks[50].TotalVolume=5 unchanged, bestAsk=50 (level never emptied), buy Filled with Remaining=0. Proves the local `cur` sweep variable correctly advances past a fully-skipped level without moving the book cursor.

## Stage 5 — REST API (2026-06-18)

**Files created:** `api/server.go`, `api/server_test.go`, `engine/query.go`, `main.go`
**Files modified:** `engine/match.go` (Submit return type), `engine/match_test.go` (submitOrder helper)

**Engine changes:**
- `Submit(*Order) ([]Trade, error)` — now returns a copy of only the fills that executed during this call. `tapeBefore := len(ob.tape)` is recorded under the exclusive lock; the slice is extracted before returning. Test helper `submitOrder` updated to discard fills with `_, err :=`.
- `engine/query.go` — adds `GetOrder(id) (OrderInfo, bool)` (RLock snapshot of Quantity/Remaining/Status) and `Snapshot(depth int) BookSnapshot` (RLock snapshot of both sides, bids high-to-low, asks low-to-high, capped by depth).

**API layer (`api/server.go`):**
- `NewServer(*engine.OrderBook) http.Handler` — wires five routes onto `http.NewServeMux()` using Go 1.22 method+path patterns.
- `POST /orders` — decodes JSON, validates owner_id/side/price/quantity, generates a server-side random order ID via `crypto/rand` (concurrent-safe), calls `Submit`, returns `{order_id, status, remaining, fills: [...]}`.
- `DELETE /orders/{id}` — calls `Cancel`, maps nil→200, `ErrNotFound`→404, `ErrAlreadyFilled`→409.
- `GET /orders/{id}` — calls `GetOrder` under RLock, returns `{quantity, remaining, status}` or 404.
- `GET /orderbook` — parses optional `?depth` param, calls `Snapshot`, returns `{best_bid, best_ask, bids, asks}` with sides pre-sorted by Snapshot.
- `GET /trades` — calls `Tape` (RLock), returns full tape as JSON array.
- All error responses are `{"error": "..."}` JSON; status strings are snake_case (`open`, `partially_filled`, `filled`, `cancelled`).

**main.go:** constructs one `OrderBook`, calls `api.NewServer`, listens on `$PORT` defaulting to `8080`.

**Tests (15 cases across 7 test functions, all pass under `go test -v -race -count=1 ./...`):**
- `TestPostOrderValidShape` — 200, all four fields present, status=open, remaining=10, fills=[].
- `TestPostOrderValidation` — 6 sub-cases (price 0, price 100, qty 0, missing owner, bad side, malformed JSON) each return 400 with `{"error":...}`.
- `TestPostOrderCross` — pre-seeds resting Sell, crosses it; asserts 1 fill with correct price/qty/maker, status=filled, remaining=0.
- `TestDeleteOrder` — POST→cancel (200), unknown ID (404), manually-filled order still in lookup (409).
- `TestGetOrder` — POST resting sell, GET by server-assigned ID (200 with correct fields), GET ghost (404).
- `TestGetOrderBook` — 3 bids + 3 asks; asserts best_bid=42/best_ask=50, bid sort 42→41→40, ask sort 50→51→52, depth=2 caps each side.
- `TestGetTrades` — seeds resting Sell, POSTs crossing Buy, asserts 1 trade with price/qty/maker/maker_owner/taker_owner/sequence.

## Stage 6 — Concurrency Tests Under Race Detector (2026-06-18)

**File created:** `engine/concurrent_test.go`

**What was built:**

No engine or handler logic was changed. Three new tests and one invariant helper were added to `package engine`.

**`checkBookInvariants(t, ob, allOrders)`** — called after `wg.Wait()` inside every concurrency test; holds `ob.mu.RLock()` while verifying:
1. No crossed book: `bestBid < bestAsk` when both are non-zero.
2. Each price level's `TotalVolume` equals the sum of `Remaining` across its resting orders (walks every `queue.Front()` to `nil`).
3. Tape total (sum of all `Trade.Quantity`) equals the sum of `(Quantity − Remaining)` across all buy orders in `allOrders`. Invariant holds because each trade of Q units fills exactly Q from one buy and Q from one sell.

**`TestConcurrentSubmits`** — 20 buyer goroutines (Buy@44–53) and 20 seller goroutines (Sell@47–56) submit concurrently against a single `OrderBook`. Prices overlap at 47–53, generating real concurrent matching. Each goroutine writes its `[]*Order` slice to a pre-allocated 2D `[][]*Order` at its own index (no mutex needed; different indices = different memory). After `wg.Wait()`, all 400 orders collected and passed to `checkBookInvariants`. Verifies no crossed book, TotalVolume correctness, and tape reconciliation.

**`TestConcurrentSubmitsAndCancels`** — 80 bids rested sequentially at prices 45–54 (`Rest`), then 20 aggressor-sell goroutines and 80 cancel goroutines all launched simultaneously. Cancel goroutines write results to pre-allocated `[]error` at unique indices (no mutex). After `wg.Wait()`, asserts each cancel returned `nil` (cancelled before match) or `ErrNotFound` (matched first); then calls `checkBookInvariants` with all 180 orders.

**`TestCancelWhileMatching`** — 500 iterations of the core race: each iteration rests a single maker Sell@50 qty=10, then races `ob.Submit(taker)` against `ob.Cancel("maker")` using a `close(ready)` start-gun channel to maximise lock contention. After `wg.Wait()` asserts exactly one of two legal outcomes:
- **Match won** (1 fill): `fills[0].Quantity==10`, `cancelErr ∈ {ErrNotFound, ErrAlreadyFilled}`, `maker.Status==Filled`, tape length=1.
- **Cancel won** (0 fills): `cancelErr==nil`, `maker.Status==Cancelled`, `taker.Status==Open`, tape length=0.
- Any other `len(fills)` fails the test as an illegal outcome (double fill etc.). Calls `checkBookInvariants` after every run.

**Race detector result:** no DATA RACE detected across all 500 iterations × both outcomes. The exclusive `sync.RWMutex.Lock()` on Submit and Cancel serialises the two operations; the test demonstrates this means exactly one of the two operations observes the maker in the book.

**Full test run:** 49/49 tests pass under `go test -v -race ./...`.

```
ok  matching-engine/api    1.476s
ok  matching-engine/engine 1.312s
```
