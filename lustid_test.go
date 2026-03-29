package lustid

import (
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
