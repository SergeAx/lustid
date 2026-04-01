// Package typed provides prefix-tagged, base58-encoded type-safe identifiers
// built on top of lustid. Each distinct ID type carries a short text prefix
// (e.g. "usr", "ord") so that identifiers are self-describing when serialised,
// while the underlying value remains a plain int64 that fits in a database
// BIGINT column.
//
// Because every ID is a lustid value, it encodes a 4-millisecond-precision
// creation timestamp. Call [ID.Timestamp] to extract it — no separate
// created_at column is required for basic time-based filtering or debugging.
//
// Declaring a new ID type requires three lines:
//
//	type userPrefix struct{}
//	func (userPrefix) Prefix() string { return "usr" }
//	type UserID = typed.ID[userPrefix]
//
// The text form "usr_jpXC4Hqyv3S" is sortable: lexicographic order of the
// base58 segment matches numeric order of the underlying int64, because the
// encoding uses a fixed 11-character width and the base58 alphabet is arranged
// in ascending ASCII order.
package typed

import (
	"database/sql/driver"
	"fmt"
	"math"
	"time"

	"serge.ax/go/lustid"
)

// alphabet is the standard base58 alphabet. Its characters appear in strictly
// ascending ASCII order (digits → uppercase → lowercase, with visually
// ambiguous characters 0 O I l removed), so lexicographic comparison of
// fixed-width encoded strings is equivalent to numeric comparison of the
// underlying values.
const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// encodedLen is the number of base58 characters required to represent any
// non-negative int64 value. ceil(log_58(2^63)) = 11.
const encodedLen = 11

// Prefix is the constraint satisfied by the phantom type parameter of ID.
// Implementations should be zero-size structs.
type Prefix interface {
	// Prefix returns the short lowercase string that will appear before the
	// underscore in the text representation, e.g. "usr".
	Prefix() string
}

// ID is a typed, prefixed identifier. P is a zero-size phantom type whose
// Prefix() method supplies the text prefix. The zero value is not a valid ID.
type ID[P Prefix] struct {
	id lustid.ID
}

// New generates a fresh ID of type ID[P].
func New[P Prefix]() ID[P] {
	return ID[P]{id: lustid.New()}
}

// Int64 returns the identifier as a plain int64.
func (id ID[P]) Int64() int64 {
	return int64(id.id)
}

// Timestamp returns the creation time encoded in the ID, truncated to 4 ms
// precision. Intended for logging and tooling, not application logic.
func (id ID[P]) Timestamp() time.Time {
	return id.id.Timestamp()
}

// prefix returns the prefix string for this type without allocating a P value
// on the heap in the common case.
func prefix[P Prefix]() string {
	var p P
	return p.Prefix()
}

// String returns the canonical text representation, e.g. "usr_jpXC4Hqyv3S".
// The base58 segment is always exactly 11 characters, ensuring that
// lexicographic ordering of String() output matches the numeric ordering of
// the underlying int64.
func (id ID[P]) String() string {
	return prefix[P]() + "_" + encodeBase58(id.Int64())
}

// Parse decodes a string produced by String(). It returns an error if the
// prefix does not match P or the base58 segment is malformed.
func Parse[P Prefix](s string) (ID[P], error) {
	pre := prefix[P]()
	expected := pre + "_"
	if len(s) <= len(expected) || s[:len(expected)] != expected {
		return ID[P]{}, fmt.Errorf("typed: expected prefix %q, got %q", pre, s)
	}
	seg := s[len(expected):]
	v, err := decodeBase58(seg)
	if err != nil {
		return ID[P]{}, fmt.Errorf("typed: %w", err)
	}
	return ID[P]{id: lustid.ID(v)}, nil
}

// MarshalText implements encoding.TextMarshaler.
// The output is identical to String().
func (id ID[P]) MarshalText() ([]byte, error) {
	return []byte(id.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
// It accepts the format produced by MarshalText / String().
func (id *ID[P]) UnmarshalText(b []byte) error {
	v, err := Parse[P](string(b))
	if err != nil {
		return err
	}
	*id = v
	return nil
}

// Value implements driver.Valuer.
// The value stored in the database is the raw int64, not the text form,
// so the column type should be BIGINT / bigint.
func (id ID[P]) Value() (driver.Value, error) {
	return id.Int64(), nil
}

// Scan implements sql.Scanner.
// It expects the database column to contain an int64-compatible value.
func (id *ID[P]) Scan(src any) error {
	switch v := src.(type) {
	case int64:
		id.id = lustid.ID(v)
		return nil
	case []byte:
		// Some drivers return numeric columns as []byte text.
		parsed, err := Parse[P](string(v))
		if err != nil {
			// Try raw int64 text representation as fallback.
			var n int64
			if _, err2 := fmt.Sscan(string(v), &n); err2 != nil {
				return fmt.Errorf("typed: cannot scan []byte %q into ID: %w", v, err)
			}
			id.id = lustid.ID(n)
			return nil
		}
		*id = parsed
		return nil
	case string:
		parsed, err := Parse[P](v)
		if err != nil {
			// Try raw int64 text representation as fallback.
			var n int64
			if _, err2 := fmt.Sscan(v, &n); err2 != nil {
				return fmt.Errorf("typed: cannot scan string %q into ID: %w", v, err)
			}
			id.id = lustid.ID(n)
			return nil
		}
		*id = parsed
		return nil
	case nil:
		return fmt.Errorf("typed: cannot scan NULL into non-pointer ID")
	default:
		return fmt.Errorf("typed: unsupported Scan source type %T", src)
	}
}

// encodeBase58 encodes a non-negative int64 as an 11-character base58 string.
// The output is left-padded with the first alphabet character ('1') so that
// all outputs have equal length and lexicographic order is preserved.
func encodeBase58(v int64) string {
	if v < 0 {
		// lustid guarantees the sign bit is always 0; this is a safeguard.
		panic(fmt.Sprintf("typed: negative value %d passed to encodeBase58", v))
	}
	u := uint64(v)
	var buf [encodedLen]byte
	for i := encodedLen - 1; i >= 0; i-- {
		buf[i] = alphabet[u%58]
		u /= 58
	}
	return string(buf[:])
}

// decodeBase58 decodes an 11-character base58 string back to an int64.
func decodeBase58(s string) (int64, error) {
	if len(s) != encodedLen {
		return 0, fmt.Errorf("base58 segment must be %d characters, got %d", encodedLen, len(s))
	}
	var u uint64
	for i := range s {
		idx := indexOf(s[i])
		if idx < 0 {
			return 0, fmt.Errorf("invalid base58 character %q at position %d", s[i], i)
		}
		// Guard against uint64 overflow before multiplying.
		if u > (math.MaxUint64-uint64(idx))/58 {
			return 0, fmt.Errorf("base58 value overflows int64")
		}
		u = u*58 + uint64(idx)
	}
	if u > math.MaxInt64 {
		return 0, fmt.Errorf("base58 value overflows int64")
	}
	return int64(u), nil
}

// indexOf returns the index of b in alphabet, or -1 if not present.
// A lookup table would be faster; this is fine for ID parsing frequency.
func indexOf(b byte) int {
	for i := range len(alphabet) {
		if alphabet[i] == b {
			return i
		}
	}
	return -1
}
