package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"roundtrip/golang/fluffy"
	"roundtrip/golang/sample"
)

// The Go side of the checks in lib/checks.dart. Every function either hands
// Dart a value Go built, so Dart can compare it with what it expects, or
// reports what Go received from Dart, so the comparison happens in Go. An
// echo alone would hide a mistake made the same way in both directions.

// ---- Types ----

// Sample is the canonical value of every kind of type.
//
//fluffy:export
func Sample() sample.Everything { return sample.Want() }

// Zero is the zero value: nil slices, maps and pointers, the zero time.
//
//fluffy:export
func Zero() sample.Everything { return sample.Everything{} }

// Check compares what Dart sent with the canonical value.
//
//fluffy:export
func Check(got sample.Everything) []string { return sample.Diff(got, sample.Want()) }

// CheckZero compares what Dart sent with the zero value.
//
//fluffy:export
func CheckZero(got sample.Everything) []string { return sample.Diff(got, sample.Everything{}) }

// Echo returns v unchanged.
//
//fluffy:export
func Echo(v sample.Everything) sample.Everything { return v }

// Nothing returns a nil pointer, null in Dart.
//
//fluffy:export
func Nothing() *sample.Item { return nil }

// Lists has nil and empty slices and maps.
type Lists struct {
	NilSlice   []string       `json:"nilSlice"`
	EmptySlice []string       `json:"emptySlice"`
	NilMap     map[string]int `json:"nilMap"`
	NilBytes   []byte         `json:"nilBytes"`
}

// NilLists returns nil slices and maps and an empty slice.
//
//fluffy:export
func NilLists() Lists { return Lists{EmptySlice: []string{}} }

// WhichNil reports which slices and maps of l arrived as nil.
//
//fluffy:export
func WhichNil(l Lists) map[string]bool {
	return map[string]bool{
		"nilSlice":   l.NilSlice == nil,
		"emptySlice": l.EmptySlice == nil,
		"nilMap":     l.NilMap == nil,
		"nilBytes":   l.NilBytes == nil,
	}
}

// ---- Numbers ----

//fluffy:export
func Int64Max() int64 { return math.MaxInt64 }

//fluffy:export
func Int64Min() int64 { return math.MinInt64 }

//fluffy:export
func Uint64Max() uint64 { return math.MaxUint64 }

// FormatInt shows the int64 Go received.
//
//fluffy:export
func FormatInt(v int64) string { return strconv.FormatInt(v, 10) }

// Floats are values whose shortest decimal form needs care.
//
//fluffy:export
func Floats() []float64 {
	return []float64{0.1, 0.30000000000000004, 1e308, 5e-324, 2.5e-8, 123456789.123, 1, math.Copysign(0, -1)}
}

// FormatFloats shows the bits of the float64 values Go received, in hex.
//
//fluffy:export
func FormatFloats(v []float64) []string {
	out := make([]string, len(v))
	for i, f := range v {
		out[i] = strconv.FormatUint(math.Float64bits(f), 16)
	}
	return out
}

// NotANumber is NaN, which cannot be encoded as JSON, so the call fails.
//
//fluffy:export
func NotANumber() float64 { return math.NaN() }

// ---- Strings and bytes ----

// Unicode is text with multi-byte runes, an emoji sequence and characters
// JSON escapes.
//
//fluffy:export
func Unicode() string {
	return "Grüße 世界 𝄞 🧑‍🚀 \u2028 </script> & \"quote\" \\ \t\n\x00\x1f"
}

// TextInfo describes the string Go received.
type TextInfo struct {
	Bytes int    `json:"bytes"`
	Runes int    `json:"runes"`
	Valid bool   `json:"valid"`
	Quote string `json:"quote"`
}

//fluffy:export
func DescribeText(s string) TextInfo {
	return TextInfo{Bytes: len(s), Runes: utf8.RuneCountInString(s), Valid: utf8.ValidString(s), Quote: strconv.QuoteToASCII(s)}
}

// InvalidUTF8 is a string that is not valid UTF-8. encoding/json replaces
// the invalid byte with U+FFFD.
//
//fluffy:export
func InvalidUTF8() string { return "a\xffb" }

// AllBytes is every byte value once.
//
//fluffy:export
func AllBytes() []byte {
	b := make([]byte, 256)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// BytesInfo describes the bytes Go received.
type BytesInfo struct {
	Len   int    `json:"len"`
	Sum   uint64 `json:"sum"`
	First int    `json:"first"`
	Last  int    `json:"last"`
}

//fluffy:export
func DescribeBytes(b []byte) BytesInfo {
	info := BytesInfo{Len: len(b), First: -1, Last: -1}
	for i, c := range b {
		info.Sum += uint64(c) * uint64(i%251+1)
	}
	if len(b) > 0 {
		info.First, info.Last = int(b[0]), int(b[len(b)-1])
	}
	return info
}

//fluffy:export
func EchoBytes(b []byte) []byte { return b }

// Repeat returns s n times.
//
//fluffy:export
func Repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

// ---- Time ----

// PreciseTime has nanoseconds and a zone offset.
//
//fluffy:export
func PreciseTime() time.Time {
	return time.Date(2024, 2, 29, 23, 59, 59, 123_456_789, time.FixedZone("", 5*3600+1800))
}

// FormatTime shows the time Go received, in UTC.
//
//fluffy:export
func FormatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// Durations are sent as nanoseconds.
//
//fluffy:export
func Durations() []time.Duration {
	return []time.Duration{1500*time.Millisecond + 999, -time.Second, math.MaxInt64}
}

// FormatDurations shows the durations Go received.
//
//fluffy:export
func FormatDurations(d []time.Duration) []string {
	out := make([]string, len(d))
	for i, v := range d {
		out[i] = strconv.FormatInt(int64(v), 10)
	}
	return out
}

// ---- Errors ----

// Fail returns an error with the message.
//
//fluffy:export
func Fail(message string) error { return errors.New(message) }

// Panic panics, the call fails instead of crashing the app.
//
//fluffy:export
func Panic(message string) error { panic(message) }

// Add adds a and b, the checks call it with arguments of the wrong number
// and type.
//
//fluffy:export
func Add(a, b int64) int64 { return a + b }

// ---- Go calls Dart ----

// Host is implemented in Dart by the checks.
//
//fluffy:dart
type Host interface {
	// Greet answers with a greeting for name.
	Greet(ctx context.Context, name string) (string, error)
	// Fail throws in Dart.
	Fail(ctx context.Context, message string) error
	// Wait returns after d.
	Wait(ctx context.Context, d time.Duration) error
	// Note is sent without waiting for Dart.
	Note(text string)
}

// AskHost has Dart greet name, a request to Dart inside a request to Go.
//
//fluffy:export
func AskHost(ctx context.Context, name string) (string, error) {
	return newHost().Greet(ctx, name)
}

// AskHostToFail returns the error of the Dart implementation.
//
//fluffy:export
func AskHostToFail(ctx context.Context, message string) error {
	err := newHost().Fail(ctx, message)
	var dartErr *fluffy.DartError
	if !errors.As(err, &dartErr) {
		return fmt.Errorf("want a *fluffy.DartError, got %T: %v", err, err)
	}
	return err
}

// AskHostWithTimeout waits for Dart at most timeout.
//
//fluffy:export
func AskHostWithTimeout(ctx context.Context, wait, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return newHost().Wait(ctx, wait)
}

// NoteHost sends the texts to Dart without waiting.
//
//fluffy:export
func NoteHost(texts []string) {
	host := newHost()
	for _, text := range texts {
		host.Note(text)
	}
}

// Event is emitted to Dart.
type Event struct {
	Seq int `json:"seq"`
}

// EmitEvents emits n events, numbered from 0, on the channel "event".
//
//fluffy:export
func EmitEvents(n int) {
	for i := 0; i < n; i++ {
		fluffy.Emit("event", Event{Seq: i})
	}
}

// Prompt asks Dart on a raw channel, answered by golang.handlePrompt.
//
//fluffy:export
func Prompt(ctx context.Context, question string) (string, error) {
	return fluffy.Request[string](ctx, "prompt", question)
}

// ---- Streams ----

// Count sends 0 to n-1.
//
//fluffy:export
func Count(ctx context.Context, n int) <-chan int {
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

// Broken fails before it sends anything.
//
//fluffy:export
func Broken(ctx context.Context) (<-chan int, error) {
	return nil, errors.New("broken stream")
}

// Empty returns a nil channel, the stream ends right away.
//
//fluffy:export
func Empty(ctx context.Context) <-chan int { return nil }

var streams struct {
	active, cancelled atomic.Int64
}

// StreamStats counts the Ticks streams.
type StreamStats struct {
	Active    int64 `json:"active"`
	Cancelled int64 `json:"cancelled"`
}

// Ticks sends a number every 10ms until Dart cancels it.
//
//fluffy:export
func Ticks(ctx context.Context) <-chan int {
	out := make(chan int)
	streams.active.Add(1)
	go func() {
		defer close(out)
		defer streams.active.Add(-1)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; ; i++ {
			select {
			case <-ticker.C:
				out <- i
			case <-ctx.Done():
				streams.cancelled.Add(1)
				return
			}
		}
	}()
	return out
}

//fluffy:export
func Stats() StreamStats {
	return StreamStats{Active: streams.active.Load(), Cancelled: streams.cancelled.Load()}
}

// ---- Concurrency ----

// Sleep returns ms after ms milliseconds.
//
//fluffy:export
func Sleep(ctx context.Context, ms int) int {
	select {
	case <-time.After(time.Duration(ms) * time.Millisecond):
	case <-ctx.Done():
	}
	return ms
}
