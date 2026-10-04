// Package bench is the Go side of the benchmarks in lib/bench.dart, which
// measure what a call, a byte and a stream value cost between Dart and Go.
// golang/main.go binds it as "bench".
package bench

import (
	"context"
	"time"
)

// Bench does as little as possible, so the time is spent in the bridge.
type Bench struct{}

// Ping does nothing.
func (Bench) Ping() {}

// Take receives b from Dart and returns its length.
func (Bench) Take(b []byte) int { return len(b) }

// Bytes sends n bytes to Dart.
func (Bench) Bytes(n int) []byte { return pattern(n) }

// Values streams the numbers 0 to n-1.
func (Bench) Values(ctx context.Context, n int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for i := 0; i < n; i++ {
			select {
			case out <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// Chunks streams n chunks of size bytes.
func (Bench) Chunks(ctx context.Context, n, size int) <-chan []byte {
	out := make(chan []byte)
	chunk := pattern(size)
	go func() {
		defer close(out)
		for i := 0; i < n; i++ {
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	return b
}

// Now is the wall clock in microseconds, to split a round trip into its
// two directions.
func (Bench) Now() int64 { return time.Now().UnixMicro() }
