# matching-engine

A thread-safe limit order book for binary outcome markets, written in Go with only the standard library. Prices are integers from 1 to 99, representing cents in a contract that settles at either 0 or 100. The engine handles matching, cancellation, and self-trade prevention, and exposes everything over a REST API.

This engine treats the market as a single instrument: buy orders match sell orders within the 1 to 99 price range. It does not implement the paired YES/NO contract model, where a YES bid at price p pairs with a NO bid at 100 minus p to mint a complete set. That variant is noted under future work.

## Why Go

Goroutines and `net/http` give a concurrent HTTP server with no external framework. `sync.RWMutex` and the race detector are exactly the tools a concurrency-sensitive matching engine needs, and the race detector is used directly in the test suite via `go test -race`. `container/list` provides the O(1) node removal that makes cancel fast. The whole engine compiles to a single static binary with no dependencies outside the standard library. The honest tradeoff: for an exchange core chasing the lowest possible latency, a language without garbage collection pauses like C++ or Rust would be a more typical choice, but Go is a strong fit for a correct, concurrent, and readable engine at this scope.

## Quick Start

```
go run main.go                        # start the server on :8080
PORT=9090 go run main.go              # override the port
go test ./...                         # run the full test suite
go test -race ./...                   # run under the race detector
go build -o matching-engine main.go   # compile a binary
```

**Worked example**

Submit a resting sell at 55:

```
curl -s -X POST http://localhost:8080/orders \
  -H 'Content-Type: application/json' \
  -d '{"owner_id":"alice","side":"sell","price":55,"quantity":10}'
```

```json
{"order_id":"3a7f2c91d4b8e065","status":"open","remaining":10,"fills":[]}
```

Cross it with a buy willing to pay up to 60:

```
curl -s -X POST http://localhost:8080/orders \
  -H 'Content-Type: application/json' \
  -d '{"owner_id":"bob","side":"buy","price":60,"quantity":10}'
```

```json
{
  "order_id": "8b1e4d72a3c90f51",
  "status": "filled",
  "remaining": 0,
  "fills": [
    {
      "price": 55,
      "quantity": 10,
      "maker_order_id": "3a7f2c91d4b8e065",
      "taker_order_id": "8b1e4d72a3c90f51"
    }
  ]
}
```

The fill prints at 55, not 60. The buy was willing to pay 60 but the resting sell only asked 55, so the trade executes at the maker's price. The buyer pays 55 instead of their limit of 60.

## API Reference

| Endpoint | Request | Response | Status codes |
|---|---|---|---|
| `POST /orders` | JSON body: `owner_id` (string), `side` ("buy" or "sell"), `price` (1–99), `quantity` (>0) | `order_id`, `status`, `remaining`, `fills` array with `price`, `quantity`, `maker_order_id`, `taker_order_id` per fill | 200, 400 |
| `DELETE /orders/{id}` | Path param `id` | `{"message":"cancelled"}` on success | 200, 404, 409 |
| `GET /orders/{id}` | Path param `id` | `quantity`, `remaining`, `status` | 200, 404 |
| `GET /orderbook` | Optional query param `depth` (positive integer, caps levels per side) | `best_bid`, `best_ask`, `bids` and `asks` arrays with `price` and `total_volume` per level. Bids sorted high to low, asks sorted low to high. | 200, 400 |
| `GET /trades` | None | Array of all executed trades: `sequence`, `price`, `quantity`, `maker_order_id`, `taker_order_id`, `maker_owner`, `taker_owner` | 200 |

Status strings in responses are `open`, `partially_filled`, `filled`, and `cancelled`. A 404 means the order ID was not found. A 409 on DELETE means the order already filled and cannot be cancelled.

Order IDs are generated server-side using `crypto/rand` and returned in the POST response. The client does not supply them.

## Design Decisions

**Integer prices.** All prices are `int64` representing whole cents. The code never uses `float32` or `float64` for prices or quantities. Floating-point rounding causes systematic errors in financial accounting, where a fill at price 33 times quantity 3 must equal exactly 99, not 98.99999... The 1 to 99 range fits trivially in int64, and the overflow concern shifts to notional value (price times quantity across many fills), which the int64 range handles for reasonable quantities.

**Fixed array indexed by price.** The order book holds two arrays of 100 slots, one for bids and one for asks, indexed directly by price. Index 0 is unused; price 1 maps to slot 1 and price 99 maps to slot 99. Because the valid range is bounded and fixed, array indexing gives constant-time access to any price level with no tree balancing, no hash collisions, and no element shifting. A general engine supporting arbitrary tick sizes would need a sorted structure like a red-black tree. This one exploits the bounded range instead.

Two integer cursors, `bestBid` and `bestAsk`, track the current best prices. They update when levels empty or new orders rest at better prices. Scanning for the next best is a linear walk over at most 99 slots, which is constant in practice.

**Monotonic sequence numbers for time priority.** Each order gets a sequence number from a counter that increments once per `Rest` or `Submit` call. Within a price level, orders fill in FIFO order based on queue position, which reflects arrival order. Wall clock timestamps introduce a tie-breaking problem: two orders arriving close together can get the same value. The monotonic counter never produces duplicates.

**Maker price execution.** When an aggressor crosses the spread, the trade executes at the resting order's price, not the aggressor's limit price. A buy at 60 that hits a sell at 55 fills at 55. This is standard limit order book behavior. The aggressor receives price improvement; the maker gets the price they posted.

**Self-trade prevention.** When the incoming order and a resting order share the same `OwnerID`, the engine skips that resting order without touching it, then continues matching the incoming order against other accounts at the same or worse prices. The skipped order stays in the queue with its quantity and status unchanged. This is the skip-the-cross policy.

The alternative of cancelling the resting order silently modifies state the owner may not expect. The alternative of rejecting the incoming order entirely prevents legitimate fills at other price levels. Skipping is the least surprising behavior and the one most consistent with the owner's expressed intent on both sides.

One subtle point: the match sweep uses a local cursor that always advances to the next price level, regardless of whether anything filled at the current level. The `bestAsk` and `bestBid` book cursors only update when a level actually empties. So when self-trade skipping leaves a level occupied, `bestAsk` stays pointing to that level (correctly, since it still has resting volume), and the sweep cursor moves past it to look for fills at worse prices within the aggressor's limit.

**Single mutex for all writes.** A single `sync.RWMutex` protects the entire order book. `Submit` and `Cancel` both take an exclusive write lock. `GetOrder`, `Snapshot`, and `Tape` take a shared read lock. The standard library HTTP server runs each request in its own goroutine, so this locking does real work on every request. The exclusive lock on `Submit` is specifically what prevents the cancel-while-matching race: once Submit acquires the lock, any concurrent Cancel either ran first (in which case Submit finds the order gone) or waits until Submit completes (in which case Cancel finds the order either filled or still resting). The race detector tests verify both outcomes occur and nothing else does.

**O(1) cancellation.** The lookup map stores a `*list.Element` for each resting order, not just the order ID. `container/list` exposes a `Remove` method that unlinks a node in constant time if you have the element pointer. The cancel path does one map lookup, then one list removal, with no scan of the price level queue.

## Edge Cases Handled

The test suite covers:

- Full fills, where both sides consume completely in one match
- Partial fills on the incoming side (aggressor larger than level, rests remainder)
- Partial fills on the resting side (smaller resting order fills, larger aggressor continues)
- Multi-level price sweeps, where an aggressor clears multiple price levels in one submit
- Time priority within a level, verified by sequence numbers
- Maker-price execution, confirmed against trade tape price
- All three self-trade variants: own order at queue front (skipped, foreign order behind it fills), own order mid-queue (both foreign orders around it fill, own order stays), and own order as sole occupant of the best price level (entire level skipped, fill happens at a worse level within the limit)
- TotalVolume accounting: skipped own orders do not decrement TotalVolume
- Cancel of a resting order, an unknown ID, a fully filled order, and a partially filled order
- Boundary validation at prices 0, 1, 99, and 100 (1 and 99 are valid; 0 and 100 are rejected)
- The cancel-while-matching race, verified 500 times under the race detector, asserting that exactly one of two outcomes occurs each time (match wins or cancel wins, never both and never a partial inconsistency)
- Two structural invariants checked after every concurrency test: the book is never crossed (no resting bid at a price greater than or equal to any resting ask), and every price level's `TotalVolume` equals the sum of `Remaining` across its resting orders

## Testing

Run the suite with `go test ./...` or under the race detector with `go test -race ./...`. There are 34 test functions covering 49 cases across the engine and API packages. All pass under the race detector.

The concurrency tests include a reconciliation helper that verifies a global accounting identity after every race: the total quantity on the trade tape equals the total quantity consumed across all buy orders (their original quantity minus remaining). This identity must hold whether operations serialized with the match winning, the cancel winning, or anything in between.

## What I Would Do With More Time

**Replace random order IDs with the sequence counter.** The engine already maintains a monotonic `seq` counter. Order IDs currently come from `crypto/rand`, which is collision-resistant but not collision-free. Using the sequence counter as the ID would make IDs deterministic, collision-proof by construction, and still unique across the lifetime of the book without the allocation of generating random bytes.

**Add per-owner position and balance tracking.** Right now the engine matches orders and records trades but does not enforce that an owner has the capital or position to back their orders. Adding a balance check before `Submit` accepts an order, and updating positions on each fill, would be the next layer toward a usable system.

**Persist and replay the trade tape.** The tape lives in memory and disappears on restart. Writing each trade to an append-only log would make crash recovery possible by replaying the log to reconstruct the book state, which is the standard approach for this class of system.

**Implement complementary set matching.** In binary markets that settle at 0 or 100, a YES bid at price p and a NO bid at 100 minus p together represent a complete set worth 100 cents. The current engine does not pair these; it only matches same-side bids against opposite-side asks. Adding this would let the engine mint contracts directly from opposing bids rather than requiring one side to post an ask first.

**Add a benchmark harness.** The hot paths are designed to be O(1): direct array indexing for price levels, O(1) cancel via the stored list element, and a single-pass match loop. But designed complexity is not the same as measured throughput. A benchmark that reports orders per second and match latency under load would show whether the single-mutex design is the right call or whether a finer-grained locking scheme is worth the added complexity.
