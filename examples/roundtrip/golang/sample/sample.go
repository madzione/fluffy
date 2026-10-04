// Package sample holds the value the roundtrip checks send between Go and
// Dart. Everything has a field for every kind of type fluffy generate maps
// to Dart, and Want is its canonical value, built the same way in Dart by
// wantEverything in lib/checks.dart. Values that do not survive the trip
// exactly (nanoseconds, large integers, nil slices) are kept out of it and
// have checks of their own.
package sample

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"time"
)

// Color is a named string type, a String in Dart.
type Color string

// Level is sent as text (encoding.TextMarshaler), a String in Dart.
type Level int

const (
	Low Level = iota
	High
)

func (l Level) MarshalText() ([]byte, error) {
	switch l {
	case Low:
		return []byte("low"), nil
	case High:
		return []byte("high"), nil
	}
	return nil, fmt.Errorf("invalid level %d", int(l))
}

func (l *Level) UnmarshalText(text []byte) error {
	switch string(text) {
	case "low":
		*l = Low
	case "high":
		*l = High
	default:
		return fmt.Errorf("invalid level %q", text)
	}
	return nil
}

// Point has its own JSON encoding (json.Marshaler), [x, y]. Dart gets the
// decoded JSON as Object?.
type Point struct{ X, Y int }

func (p Point) MarshalJSON() ([]byte, error) { return json.Marshal([2]int{p.X, p.Y}) }

func (p *Point) UnmarshalJSON(data []byte) error {
	var xy [2]int
	if err := json.Unmarshal(data, &xy); err != nil {
		return err
	}
	p.X, p.Y = xy[0], xy[1]
	return nil
}

// Item is a plain struct.
type Item struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

// Page is a generic struct, Page[Item] is the Dart class PageItem.
type Page[T any] struct {
	Items []T     `json:"items"`
	Next  *string `json:"next"`
}

// Base is embedded, its fields are promoted.
type Base struct {
	Kind    string `json:"kind"`
	Version int    `json:"version"`
}

// Audit is embedded as a pointer, its fields are optional.
type Audit struct {
	CreatedBy string `json:"createdBy"`
}

// Everything has a field for each kind of type.
type Everything struct {
	Base
	*Audit

	Bool     bool             `json:"bool"`
	Int      int              `json:"int"`
	Int8     int8             `json:"int8"`
	Uint16   uint16           `json:"uint16"`
	Int64    int64            `json:"int64"`
	Float32  float32          `json:"float32"`
	Float64  float64          `json:"float64"`
	String   string           `json:"string"`
	Color    Color            `json:"color"`
	Level    Level            `json:"level"`
	Point    Point            `json:"point"`
	Bytes    []byte           `json:"bytes"`
	Time     time.Time        `json:"time"`
	Duration time.Duration    `json:"duration"`
	Addr     netip.Addr       `json:"addr"`
	Array    [3]int           `json:"array"`
	Strings  []string         `json:"strings"`
	Items    []Item           `json:"items"`
	Ptrs     []*Item          `json:"ptrs"`
	Matrix   [][]float64      `json:"matrix"`
	ByName   map[string]Item  `json:"byName"`
	ByID     map[int]string   `json:"byId"`
	Nested   map[string][]int `json:"nested"`
	Page     Page[Item]       `json:"page"`
	Child    *Item            `json:"child"`
	Any      any              `json:"any"`
	Optional string           `json:"optional,omitempty"`
	Quoted   int64            `json:"quoted,string"`
	Untagged string
	Skipped  string `json:"-"`
	hidden   int
}

// Want is the canonical value. Times and durations are whole milliseconds
// and integers stay below 2^53, so it is exact on every platform.
func Want() Everything {
	next := "page-2"
	return Everything{
		Base:     Base{Kind: "sample", Version: 3},
		Audit:    &Audit{CreatedBy: "go"},
		Bool:     true,
		Int:      -42,
		Int8:     -128,
		Uint16:   65535,
		Int64:    1<<53 - 1,
		Float32:  0.1,
		Float64:  -1234.5678,
		String:   "Grüße, 世界 🧑‍🚀",
		Color:    "teal",
		Level:    High,
		Point:    Point{X: 3, Y: -4},
		Bytes:    []byte{0, 1, 2, 127, 128, 254, 255},
		Time:     time.Date(2024, 2, 29, 23, 59, 59, 125_000_000, time.FixedZone("IST", 5*3600+1800)),
		Duration: 90*time.Minute + 250*time.Millisecond,
		Addr:     netip.MustParseAddr("2001:db8::1"),
		Array:    [3]int{1, 2, 3},
		Strings:  []string{"a", "", "c"},
		Items:    []Item{{ID: 1, Title: "one"}, {ID: 2, Title: "two", Done: true}},
		Ptrs:     []*Item{{ID: 3, Title: "three"}, nil},
		Matrix:   [][]float64{{1, 0.5}, {}, {-2.25}},
		ByName:   map[string]Item{"x": {ID: 4, Title: "x"}, "y": {ID: 5, Title: "y"}},
		ByID:     map[int]string{-1: "minus one", 7: "seven"},
		Nested:   map[string][]int{"empty": {}, "primes": {2, 3, 5}},
		Page:     Page[Item]{Items: []Item{{ID: 6, Title: "six"}}, Next: &next},
		Child:    &Item{ID: 8, Title: "child"},
		Any:      map[string]any{"list": []any{1.0, "two", nil, true}},
		Optional: "set",
		Quoted:   9007199254740991,
		Untagged: "untagged",
	}
}
