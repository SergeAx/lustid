# Typed LUSTID

A Go package for declaring type-safe, self-describing identifiers on top of
[lustid](https://serge.ax/go/lustid). Each ID type carries a short text prefix
so that identifiers are recognisable when serialised, while the underlying
value remains a plain `int64` that fits in a `BIGINT` column and sorts
correctly in any B-tree index.

Because every value is a lustid, it encodes a 4-millisecond-precision creation
timestamp. Call `id.Timestamp()` to retrieve it — no separate `created_at`
column is required for basic time-based filtering or debugging.

```text
usr_jpXC4Hqyv3S   ← UserID
ord_jpXD7Rzab2K   ← OrderID
```

## Design

### The problem with untyped IDs

When every entity in a system uses the same `int64` or `lustid.ID` type, the
compiler cannot distinguish a user ID from an order ID. Passing the wrong ID
to the wrong function is a silent bug. Seeing a bare integer in a log or API
response gives no indication of what kind of entity it refers to.

### The solution: phantom types + prefix

`typed.ID[P]` is a generic wrapper around `lustid.ID`. The type parameter `P`
is a zero-size phantom type whose only job is to carry a short prefix string.
The compiler enforces that `UserID` and `OrderID` are distinct types — you
cannot pass one where the other is expected. The prefix is visible in the text
representation but absent from the stored value, so there is no storage
overhead and no schema changes required.

### Text representation

The text form is `<prefix>_<base58>`, where the base58 segment is always
exactly 11 characters. Fixed width is required for sortability: because the
base58 alphabet (`123456789ABC...Zabc...z`) is arranged in ascending ASCII
order, lexicographic comparison of two same-prefix IDs is equivalent to
numeric comparison of the underlying `int64` values.

```text
usr_1111111111  <  usr_jpXC4Hqyv3S  <  usr_NZt5uGsmMab
```

The prefix portion ensures IDs of different types never compare equal and sort
into clearly separated ranges.

### Embedded timestamp

Every `typed.ID` is a lustid value: the upper 40 bits encode a tick counter
derived from wall-clock time (4 ms resolution, epoch 2026-01-01 UTC), and the
lower 23 bits are a random-seeded sequence counter. This means:

- IDs sort chronologically by construction — no `ORDER BY created_at` needed
  when querying by primary key.
- `id.Timestamp()` returns the creation time to 4 ms precision, useful for
  logs, dashboards, and time-range queries without an extra column.

### Storage

The database column stays a plain `BIGINT`. The prefix lives only in the
application layer. `driver.Valuer` stores the raw `int64`; `sql.Scanner`
reads it back. The text form is used for JSON, URLs, and logs — not for
database storage.

## Requirements

Go 1.21 or later (for generic type inference). Depends on
`serge.ax/go/lustid`; no other external dependencies.

## Installation

```bash
go get serge.ax/go/lustid
```

The `typed` package is part of the same module and requires no separate `go get`.

## Declaring a new ID type

All references to `typed` (and `lustid`) should be confined to a single
declaration file — for example `ids.go` — in the package that owns each
entity. The rest of your codebase imports nothing from this module; it only
sees `UserID`, `NewUserID`, and `ParseUserID`.

```go
// ids.go — the only file in your package that imports typed.

import "serge.ax/go/lustid/typed"

type userPrefix struct{}
func (userPrefix) Prefix() string { return "usr" }

type UserID = typed.ID[userPrefix]

func NewUserID() UserID                    { return typed.New[userPrefix]() }
func ParseUserID(s string) (UserID, error) { return typed.Parse[userPrefix](s) }
```

`typed` becomes a build-time implementation detail. No call site outside
`ids.go` ever names it, so swapping the underlying ID library later is a
one-file change.

The prefix struct is conventionally unexported. The type alias (`=`) is
important: it makes `UserID` and `typed.ID[userPrefix]` the same type, so
`UserID` inherits all methods (`String`, `MarshalText`, `Value`, `Scan`, …)
without any forwarding code and without any extra import at the call site.

Keep prefixes short (2–5 characters), lowercase, and stable. A prefix change
is a breaking change for any stored or transmitted text IDs.

**Prefixes must be unique across all ID types in your application.** Two types
sharing a prefix is a silent bug: their text representations become
indistinguishable, and `Parse` will accept one where the other is expected.
This package does not enforce uniqueness — it is your responsibility to ensure
no two prefix structs return the same string. The simplest safeguard is to
declare all ID types in a single file so duplicates are immediately visible.

## Usage

The examples below assume the declarations from the section above. Nothing
outside `ids.go` imports `typed` or `lustid`.

### Generation

```go
id := NewUserID()
fmt.Println(id) // usr_jpXC4Hqyv3S
```

### Parsing

```go
id, err := ParseUserID("usr_jpXC4Hqyv3S")
if err != nil {
    // wrong prefix, wrong length, or invalid characters
}
```

`ParseUserID` delegates to `typed.Parse`, which returns an error if the
prefix does not match, so passing an `OrderID` string where a `UserID` is
expected fails at runtime as well as at compile time.

### In a database model

```go
type User struct {
    ID    UserID `db:"id"`
    Email string `db:"email"`
}

func CreateUser(db *sql.DB, email string) (User, error) {
    u := User{
        ID:    NewUserID(),
        Email: email,
    }
    // Value() stores the raw int64; the column type is BIGINT.
    _, err := db.Exec(`INSERT INTO users (id, email) VALUES ($1, $2)`, u.ID, u.Email)
    return u, err
}

func GetUser(db *sql.DB, id UserID) (User, error) {
    var u User
    // Scan() reads the int64 back from the database.
    err := db.QueryRow(`SELECT id, email FROM users WHERE id = $1`, id).
        Scan(&u.ID, &u.Email)
    return u, err
}
```

### JSON

`typed.ID` implements `encoding.TextMarshaler` and `encoding.TextUnmarshaler`,
so `json.Marshal` and `json.Unmarshal` use the prefixed text form automatically.

```go
type Response struct {
    UserID UserID `json:"user_id"`
}
// {"user_id":"usr_jpXC4Hqyv3S"}
```

### Inspecting the timestamp

Every ID carries a creation timestamp encoded in its upper bits (4 ms
precision). No database column or separate query is needed:

```go
fmt.Println(id.Timestamp()) // e.g. 2026-03-15 10:42:07.804 +0000 UTC
```

## Database schema

No changes from plain lustid. Use a standard 64-bit integer column.

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

## Limitations

**Prefix stability.** The prefix is not stored in the database. If you rename
a prefix, existing rows will scan back correctly but their text representation
will change. Treat prefixes as part of your public API.

**Runtime prefix check only for `Scan`.** When reading from the database,
`Scan` receives a raw `int64` with no prefix information. The prefix is
re-attached by the type; there is no way to verify at scan time that the
column contained a value originally generated as a `UserID` rather than an
`OrderID`. Enforce this at the query level, not the scan level.

**Not a capability.** As with all lustid-based identifiers, knowing an ID does
not grant access to the associated resource. Enforce ownership checks in
queries. See the lustid [security considerations](../README.md#security-considerations).

## License

[MIT](../LICENSE)
