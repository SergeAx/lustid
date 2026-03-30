# Locally Unique SorTable IDentifier

A Go package for generating 64-bit identifiers that are safe to expose in
URLs, friendly to database indexes, and require no coordination between
generators.

> **On the name:** LUSTID is a backronym, and yes, it is deliberate. Life is
> too short for boring package names, and at least it is memorable. If your
> workplace has a strict policy on the matter, you are welcome to alias it.

## Background and Design Rationale

### The problem with common approaches

**Auto-increment integers** are fast and index-friendly, but expose business
intelligence: anyone who creates two records and compares their IDs can infer
how many records exist and at what rate they are created. They also cannot be
generated outside the database, which complicates distributed or offline
scenarios.

**Random UUIDs (v4)** are opaque and decentralised, but are 128-bit random
values — and randomness is the enemy of B-tree indexes. Each insert lands at
a random position in the index, causing page splits, index bloat, and cache
misses. At scale, this can increase storage by 2× and query latency by orders
of magnitude compared to sequential keys.

### The solution: time-prefix + monotonic counter

The key insight is to separate two concerns:

1. **Index performance** depends on *insertion order*, not on the value being
   predictable to humans.
2. **Opacity** requires only that the ID not trivially reveal counts or rates
   — not that it be cryptographically secret.

This package generates 64-bit IDs structured as follows:

```text
 63 62       23       0
  |  |        |       |
  [1][40 bits][23 bits]
   ^ timestamp  counter
   sign bit (always 0)
```

<!-- markdownlint-disable MD013 -->
| Bits | Field     | Detail |
|------|-----------|--------|
|   1  | Sign bit  | Always 0; required for compatibility with Go's `int64` and SQL drivers |
|   40 | Timestamp | Milliseconds since custom epoch, in 4 ms ticks (~139 years of range) |
|   23 | Counter   | Randomly seeded each tick, incremented by a random step per ID |
<!-- markdownlint-enable MD013 -->

**Why 4 ms ticks instead of 1 ms?** Trading 2 bits of timestamp resolution
for 2 extra counter bits. The range is identical (~139 years from the epoch),
but the counter space is 4× larger, providing more resistance to within-tick
guessing.

**Why a randomly seeded counter?** This follows
[UUIDv7 Method 3](https://www.rfc-editor.org/rfc/rfc9562#section-6.2).
Seeding the counter randomly at the start of each tick means an attacker who
observes two IDs from the same tick learns the *difference* (how many IDs
were generated between them) but not the *absolute count* (how many IDs
existed before them). Combined with the timestamp's coarse 4 ms resolution,
this meaningfully limits volume inference.

**Why not node IDs (à la Snowflake)?** Snowflake-style node IDs solve
distributed uniqueness via coordination — each generator is assigned a unique
node ID. The random counter achieves the same goal probabilistically: with 23
bits of per-tick counter space (~8 million slots per 4 ms), the probability of
two independent generators colliding is negligible at any realistic throughput.
This is the same bet UUID makes, at a smaller scale where the maths is even
more favourable.

### ID properties

- **Sortable**: IDs generated later are always larger. Within a tick, counter
  increments preserve ordering for IDs generated in the same process.
- **Index-friendly**: Monotonically increasing values append to the end of
  B-tree indexes, avoiding page splits and fragmentation.
- **Opaque**: No auto-increment sequence is visible. Having two IDs doesn't
  grant insight into object creation volume inbetween.
- **64-bit**: Fits in a Go `int64`, a PostgreSQL `bigint`, a MySQL `BIGINT`,
  and a JSON number without precision loss.
- **No coordination required**: IDs can be generated in any process without a
  shared sequence or node ID registry.
- **Max value**: The maximum ID equals `math.MaxInt64` — the full positive
  range of `int64` is used with nothing wasted.

### Security considerations

The ID is **not a capability**. Knowing an ID should not be sufficient to
access the associated resource. Always enforce ownership checks in queries
(`WHERE id = ? AND owner_id = ?`). With proper authorisation, the timestamp
embedded in an ID is at most a minor information disclosure.

If you need an identifier that *is* a capability — shared access links,
password reset tokens, API keys — use a separate full-random token for that
purpose. Do not use these IDs for that role.

## Performance

Measured on an AMD Ryzen 5 3500X (6-core), Go 1.25, Windows 10:

```text
BenchmarkNew-6            ~29 ns/op   0 B/op   0 allocs/op
BenchmarkNewParallel-6    ~49 ns/op   0 B/op   0 allocs/op
```

`BenchmarkNew` generates IDs sequentially from a single goroutine.
`BenchmarkNewParallel` saturates all cores via `b.RunParallel`. The generator
is contention-bound in the parallel case; the single global mutex is the
intended trade-off for strict global monotonicity.

## Requirements

Go 1.25 or later. The package uses `math/rand/v2` (`ChaCha8`) introduced in
Go 1.22 and the test suite uses `WaitGroup.Go` and `range`-over-int, both
available from Go 1.25.

## Installation

```bash
go get serge.ax/go/lustid
```

## Usage

### Basic generation

```go
package main

import (
    "fmt"
    "serge.ax/go/lustid"
)

func main() {
    id := lustid.New()
    fmt.Println(id)            // e.g. 00e3f1a2b4c5d6e7
    fmt.Println(id.Timestamp()) // e.g. 2026-03-28 14:23:01.234 +0000 UTC
}
```

### In a database model

```go
type User struct {
    ID    lustid.ID `db:"id"`
    Email string    `db:"email"`
}

func CreateUser(db *sql.DB, email string) (User, error) {
    u := User{
        ID:    lustid.New(),
        Email: email,
    }
    _, err := db.Exec(`INSERT INTO users (id, email) VALUES ($1, $2)`,
        int64(u.ID), u.Email)
    return u, err
}
```

### As a trace ID

Because IDs are sortable, they are well-suited for trace or correlation IDs:
events from the same request or workflow will cluster together in time-ordered
logs without any additional sorting step.

```go
func Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Honor a trace ID propagated by an upstream caller,
        // generate one otherwise.
        traceID := r.Header.Get("X-Trace-Id")
        if traceID == "" {
            traceID = lustid.New().String() // e.g. "00e3f1a2b4c5d6e7"
        }
        ctx := context.WithValue(r.Context(), traceKey, traceID)
        w.Header().Set("X-Trace-Id", traceID)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

### Extracting the timestamp (debugging)

`ID` carries a `Timestamp()` method for internal tooling and log inspection:

```go
id := lustid.New()
fmt.Println(id.Timestamp()) // e.g. 2026-03-28 14:23:01.234 +0000 UTC
```

> Do not surface this in APIs — it confirms to callers that the timestamp is
> embedded.

## Database schema

Use a standard 64-bit integer column. No extension or special type is
required.

### PostgreSQL

```sql
CREATE TABLE users (
    id BIGINT PRIMARY KEY,
    -- ...
);
```

### MySQL

```sql
CREATE TABLE users (
    id BIGINT NOT NULL PRIMARY KEY,
    -- ...
);
```

Because IDs are monotonically increasing, the primary key index behaves
identically to an auto-increment column from the database's perspective.

## Limitations

- **Single-process generation only** (without modification). The package uses
  an in-process mutex. If you generate IDs in multiple independent processes
  simultaneously, collisions are possible but remain highly unlikely due to
  the random counter seed. If you require a hard uniqueness guarantee across
  many concurrent generators, carve a few bits from the counter for a
  generator ID — but test whether you actually need this before adding the
  complexity.
- **Clock dependency**. IDs depend on the system clock. A clock moving
  backwards (e.g. after an NTP correction) will not cause incorrect behaviour
  — the package will reuse the last tick — but it will reduce the effective
  randomness of IDs generated during that window.
- **Not a secret**. The timestamp is recoverable by anyone who holds an ID.
  Do not rely on ID opacity as a security control.

## Alternatives considered

<!-- markdownlint-disable MD013 -->
| Scheme | Bits | Sortable | Opaque | Notes |
|--------|------|----------|--------|-------|
| Auto-increment | 64 | ✅ | ❌ | Leaks record counts; DB-generated only |
| UUIDv4 | 128 | ❌ | ✅ | Poor index performance; 2× storage overhead |
| UUIDv7 | 128 | ✅ | ✅ | Best choice if 128 bits is acceptable |
| Snowflake | 64 | ✅ | Partial | Requires node ID coordination |
| **lustid** | **64** | **✅** | **✅** | No coordination; fits `int64` natively |
<!-- markdownlint-enable MD013 -->

If 128 bits is acceptable in your system, prefer UUIDv7 — it is a ratified
standard with broad library support and achieves the same goals with more
randomness headroom.

## License

[MIT](LICENSE)
