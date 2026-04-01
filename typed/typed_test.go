package typed_test

import (
	"encoding/json"
	"slices"
	"testing"

	"serge.ax/go/lustid/typed"
)

// --- Example domain types ---------------------------------------------------

type userPrefix struct{}

func (userPrefix) Prefix() string { return "usr" }

type UserID = typed.ID[userPrefix]

type orderPrefix struct{}

func (orderPrefix) Prefix() string { return "ord" }

type OrderID = typed.ID[orderPrefix]

// --- String / Parse ---------------------------------------------------------

func TestStringFormat(t *testing.T) {
	id := typed.New[userPrefix]()
	s := id.String()

	const pfx = "usr_"
	if len(s) <= len(pfx) || s[:len(pfx)] != pfx {
		t.Fatalf("expected usr_ prefix, got %q", s)
	}
	seg := s[len(pfx):]
	if len(seg) != 11 {
		t.Fatalf("expected 11-char base58 segment, got %d chars: %q", len(seg), seg)
	}
}

func TestParseRoundTrip(t *testing.T) {
	for range 10_000 {
		original := typed.New[userPrefix]()
		s := original.String()
		recovered, err := typed.Parse[userPrefix](s)
		if err != nil {
			t.Fatalf("Parse failed: %v", err)
		}
		if recovered != original {
			t.Fatalf("round-trip mismatch: %v → %q → %v", original, s, recovered)
		}
	}
}

func TestParseWrongPrefix(t *testing.T) {
	id := typed.New[orderPrefix]()
	_, err := typed.Parse[userPrefix](id.String())
	if err == nil {
		t.Fatal("expected error when parsing OrderID as UserID")
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []string{
		"",
		"usr_",
		"usr_tooshort",
		"usr_toolongvalue!!",
		"usr_000000000O0", // 'O' not in base58 alphabet
	}
	for _, s := range cases {
		_, err := typed.Parse[userPrefix](s)
		if err == nil {
			t.Errorf("expected error for input %q", s)
		}
	}
}

// --- Sortability ------------------------------------------------------------

func TestSortability(t *testing.T) {
	const n = 1000
	ids := make([]UserID, n)
	for i := range ids {
		ids[i] = typed.New[userPrefix]()
	}

	// IDs are generated in order; their string representations must sort the
	// same way as they were generated.
	strs := make([]string, n)
	for i, id := range ids {
		strs[i] = id.String()
	}

	if !slices.IsSorted(strs) {
		// Find first violation for a useful error message.
		for i := 1; i < n; i++ {
			if strs[i] < strs[i-1] {
				t.Fatalf("sort order violated at index %d: %q > %q", i-1, strs[i-1], strs[i])
			}
		}
	}
}

func TestSortabilityBoundaryValues(t *testing.T) {
	// Encode known int64 values and verify their string representations sort correctly.
	cases := []int64{0, 1, 57, 58, 58*58 - 1, 58 * 58, 1<<32 - 1, 1 << 32, 1<<62 - 1, 1 << 62, 1<<63 - 1}
	ids := make([]UserID, len(cases))
	for i, v := range cases {
		if err := ids[i].Scan(v); err != nil {
			t.Fatalf("Scan(%d): %v", v, err)
		}
	}
	for i := 1; i < len(ids); i++ {
		if ids[i].String() <= ids[i-1].String() {
			t.Fatalf("boundary sort violated: %d (%q) should be > %d (%q)",
				cases[i], ids[i].String(), cases[i-1], ids[i-1].String())
		}
	}
}

// --- MarshalText / UnmarshalText --------------------------------------------

func TestMarshalText(t *testing.T) {
	id := typed.New[userPrefix]()
	b, err := id.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	var id2 UserID
	if err := id2.UnmarshalText(b); err != nil {
		t.Fatal(err)
	}
	if id != id2 {
		t.Fatalf("MarshalText round-trip failed: %v != %v", id, id2)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	type payload struct {
		UserID UserID `json:"user_id"`
	}
	original := payload{UserID: typed.New[userPrefix]()}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var recovered payload
	if err := json.Unmarshal(b, &recovered); err != nil {
		t.Fatal(err)
	}
	if original != recovered {
		t.Fatalf("JSON round-trip failed: %v != %v", original, recovered)
	}
	// Confirm the JSON looks right, e.g. {"user_id":"usr_jpXC4Hqyv3S"}
	s := string(b)
	if len(s) < 16 || s[12:16] != "usr_" {
		t.Errorf("unexpected JSON format: %s", s)
	}
}

// --- sql.Scanner / driver.Valuer --------------------------------------------

func TestValueScanRoundTrip(t *testing.T) {
	id := typed.New[userPrefix]()

	// Value() → int64
	v, err := id.Value()
	if err != nil {
		t.Fatal(err)
	}
	n, ok := v.(int64)
	if !ok {
		t.Fatalf("Value() returned %T, want int64", v)
	}

	// Scan from int64
	var recovered UserID
	if err := recovered.Scan(n); err != nil {
		t.Fatal(err)
	}
	if id != recovered {
		t.Fatalf("Scan(int64) round-trip failed: %v != %v", id, recovered)
	}
}

func TestScanFromString(t *testing.T) {
	id := typed.New[userPrefix]()
	var recovered UserID
	if err := recovered.Scan(id.String()); err != nil {
		t.Fatalf("Scan(string) failed: %v", err)
	}
	if id != recovered {
		t.Fatalf("Scan(string) round-trip failed: %v != %v", id, recovered)
	}
}

func TestScanFromBytes(t *testing.T) {
	id := typed.New[userPrefix]()
	var recovered UserID
	if err := recovered.Scan([]byte(id.String())); err != nil {
		t.Fatalf("Scan([]byte) failed: %v", err)
	}
	if id != recovered {
		t.Fatalf("Scan([]byte) round-trip failed: %v != %v", id, recovered)
	}
}

func TestScanNilErrors(t *testing.T) {
	var id UserID
	if err := id.Scan(nil); err == nil {
		t.Fatal("expected error when scanning nil into non-pointer ID")
	}
}

// --- Type safety (compile-time) ---------------------------------------------
// The following would not compile if uncommented, demonstrating that UserID
// and OrderID are distinct types:
//
// func typeCheck() {
//     var u UserID
//     var o OrderID
//     u = o  // cannot use o (variable of type OrderID) as UserID
//     _ = u
// }
