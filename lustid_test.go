package lustid

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestNew_Uniqueness(t *testing.T) {
	const n = 10_000
	seen := make(map[ID]struct{}, n)
	for i := range n {
		id := New()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID after %d iterations: %d", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestNew_Monotonic(t *testing.T) {
	prev := New()
	for range 1000 {
		cur := New()
		if cur <= prev {
			t.Fatalf("non-monotonic: prev=%d cur=%d", prev, cur)
		}
		prev = cur
	}
}

func TestNew_Positive(t *testing.T) {
	for range 1000 {
		id := New()
		if int64(id) < 0 {
			t.Fatalf("negative ID: %d", id)
		}
	}
}

func TestNew_ConcurrentUnique(t *testing.T) {
	const goroutines = 20
	const perGoroutine = 500

	ch := make(chan ID, goroutines*perGoroutine)
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range perGoroutine {
				ch <- New()
			}
		})
	}
	wg.Wait()
	close(ch)

	seen := make(map[ID]struct{}, goroutines*perGoroutine)
	for id := range ch {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID in concurrent test: %d", id)
		}
		seen[id] = struct{}{}
	}
}

func TestTimestamp_RoundTrip(t *testing.T) {
	before := time.Now().Truncate(time.Duration(tickMs) * time.Millisecond)
	id := New()
	after := time.Now()

	ts := id.Timestamp()
	if ts.Before(before) {
		t.Errorf("timestamp %v before generation start %v", ts, before)
	}
	if ts.After(after.Add(time.Duration(tickMs) * time.Millisecond)) {
		t.Errorf("timestamp %v too far after generation end %v", ts, after)
	}
}

func TestTimestamp_Precision(t *testing.T) {
	id := New()
	ts := id.Timestamp()
	if ts.UnixMilli()%tickMs != 0 {
		t.Errorf("timestamp %v not aligned to %d ms tick", ts, tickMs)
	}
}

func TestMaxID_Value(t *testing.T) {
	// MaxID must be exactly math.MaxInt64
	const want = ID(1<<63 - 1)
	if MaxID != want {
		t.Errorf("MaxID = %d, want %d", MaxID, want)
	}
}

func TestNew_AfterCustomEpoch(t *testing.T) {
	epoch := time.UnixMilli(CustomEpochMs)
	id := New()
	if id.Timestamp().Before(epoch) {
		t.Errorf("ID timestamp %v is before custom epoch %v", id.Timestamp(), epoch)
	}
}

func TestInit_RNGSeeded(t *testing.T) {
	if rng == nil {
		t.Fatal("rng not seeded in init()")
	}
}

func TestNew_MonotonicConcurrentPerGoroutine(t *testing.T) {
	const goroutines = 20
	const perGoroutine = 1000
	results := make([][]ID, goroutines)
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Go(func() {
			ids := make([]ID, perGoroutine)
			for i := range ids {
				ids[i] = New()
			}
			results[g] = ids
		})
	}
	wg.Wait()
	for g, ids := range results {
		for i := 1; i < len(ids); i++ {
			if ids[i] <= ids[i-1] {
				t.Fatalf("goroutine %d: non-monotonic at i=%d: %d <= %d", g, i, ids[i], ids[i-1])
			}
		}
	}
}

func TestString_Format(t *testing.T) {
	id := New()
	s := id.String()
	if len(s) != 16 {
		t.Fatalf("String() len = %d, want 16: %q", len(s), s)
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("String() contains non-hex char %q: %s", c, s)
		}
	}
}

func TestParse_RoundTrip(t *testing.T) {
	id := New()
	got, err := Parse(id.String())
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", id.String(), err)
	}
	if got != id {
		t.Fatalf("round-trip mismatch: got %d, want %d", got, id)
	}
}

func TestParse_Errors(t *testing.T) {
	cases := []string{"", "abc", "gg00000000000000", "00000000000000000", "8000000000000000"}
	for _, s := range cases {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", s)
		}
	}
}

func TestNew_CounterExhaustion(t *testing.T) {
	// Force counter to the top of the space so any step will wrap.
	mu.Lock()
	now := time.Now().UnixMilli()
	savedLastTick := lastTick
	savedCounter := counter
	lastTick = (now - CustomEpochMs) / tickMs
	counter = counterMask
	mu.Unlock()
	defer func() {
		mu.Lock()
		lastTick = savedLastTick
		counter = savedCounter
		mu.Unlock()
	}()

	id := New()

	if int64(id) < 0 {
		t.Fatalf("negative ID after counter exhaustion: %v", id)
	}
	// The ID must have come from a fresh tick; verify subsequent IDs are still monotonic.
	prev := id
	for range 10 {
		cur := New()
		if cur <= prev {
			t.Fatalf("non-monotonic after counter exhaustion: %v <= %v", cur, prev)
		}
		prev = cur
	}
}

func TestMustParse_Valid(t *testing.T) {
	id := New()
	if got := MustParse(id.String()); got != id {
		t.Fatalf("MustParse round-trip: got %v, want %v", got, id)
	}
}

func TestMustParse_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustParse did not panic on invalid input")
		}
	}()
	MustParse("invalid")
}

func TestMarshalText_RoundTrip(t *testing.T) {
	id := New()
	b, err := id.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	var got ID
	if err := got.UnmarshalText(b); err != nil {
		t.Fatalf("UnmarshalText: %v", err)
	}
	if got != id {
		t.Fatalf("round-trip mismatch: got %v, want %v", got, id)
	}
}

func TestUnmarshalText_Errors(t *testing.T) {
	cases := []string{"", "abc", "gg00000000000000", "8000000000000000"}
	for _, s := range cases {
		var id ID
		if err := id.UnmarshalText([]byte(s)); err == nil {
			t.Errorf("UnmarshalText(%q) expected error, got nil", s)
		}
	}
}

func TestJSON_RoundTrip(t *testing.T) {
	id := New()
	b, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `"` + id.String() + `"`
	if string(b) != want {
		t.Fatalf("json.Marshal = %s, want %s", b, want)
	}
	var got ID
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got != id {
		t.Fatalf("JSON round-trip mismatch: got %v, want %v", got, id)
	}
}

func TestJSON_InStruct(t *testing.T) {
	type row struct {
		ID   ID     `json:"id"`
		Name string `json:"name"`
	}
	in := row{ID: New(), Name: "test"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out row
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if out.ID != in.ID || out.Name != in.Name {
		t.Fatalf("struct round-trip mismatch: got %+v, want %+v", out, in)
	}
}

func BenchmarkNew(b *testing.B) {
	for b.Loop() {
		New()
	}
}

func BenchmarkNewParallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			New()
		}
	})
}
