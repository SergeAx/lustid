package lustid

import (
    "crypto/rand"
    mrand "math/rand/v2"
    "sync"
    "time"
)

// ID is a locally unique, sortable, opaque 64-bit identifier.
// It is a named type over int64 and can be cast freely: int64(id), lustid.ID(n).
type ID int64

const (
    // CustomEpochMs is the millisecond Unix timestamp of the custom epoch:
    // 2026-01-01 00:00:00 UTC.
    CustomEpochMs = int64(1767225600000)

    tickMs      = 4 // milliseconds per tick
    counterBits = 23
    counterMask = (1 << counterBits) - 1 // 0x7FFFFF

    // MaxID is the largest possible ID, equal to math.MaxInt64.
    MaxID = ID((1<<40-1)<<counterBits | counterMask)
)

var (
    mu       sync.Mutex
    rng      *mrand.ChaCha8
    lastTick int64
    counter  int64
)

func init() {
    var seed [32]byte
    if _, err := rand.Read(seed[:]); err != nil {
        panic("lustid: crypto/rand unavailable: " + err.Error())
    }
    rng = mrand.NewChaCha8(seed)
}

func randomInt64() int64 {
    return int64(rng.Uint64())
}

// New returns a new unique, sortable, opaque ID.
// It is safe for concurrent use.
func New() ID {
    mu.Lock()
    defer mu.Unlock()

    now := time.Now().UnixMilli()
    tick := (now - CustomEpochMs) / tickMs

    if tick > lastTick {
        // New tick: seed the counter at a random position within the tick's space.
        lastTick = tick
        counter = randomInt64() & counterMask
    } else {
        // Same tick: advance by a random step (1-8) to obscure within-tick volume.
        step := (randomInt64() & 0x7) + 1
        counter = (counter + step) & counterMask

        // Counter exhausted. This should never happen at normal throughput
        // (~8 million IDs per 4 ms window). Wait for the next tick.
        if counter < step {
            time.Sleep(time.Duration(tickMs) * time.Millisecond)
            tick = (time.Now().UnixMilli() - CustomEpochMs) / tickMs
            lastTick = tick
            counter = randomInt64() & counterMask
        }
    }

    return ID((tick << counterBits) | counter)
}

// Timestamp returns the creation time encoded in the ID, truncated to 4 ms precision.
// Intended for internal tooling and log inspection only.
func (id ID) Timestamp() time.Time {
    tick := int64(id) >> counterBits
    ms := tick*tickMs + CustomEpochMs
    return time.UnixMilli(ms)
}
