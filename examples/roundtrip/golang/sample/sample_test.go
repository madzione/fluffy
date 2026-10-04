package sample

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/want.json")

// testdata/want.json is Want as JSON. test/decode_test.dart decodes it with
// the generated Dart classes, so the Dart side is checked without Go.
func TestWantJSON(t *testing.T) {
	got, err := json.MarshalIndent(Want(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *update {
		if err := os.WriteFile("testdata/want.json", got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/want.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("testdata/want.json is out of date, run go test ./sample -update")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	for name, value := range map[string]Everything{"want": Want(), "zero": {}} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var back Everything
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatal(err)
		}
		if diffs := Diff(back, value); len(diffs) > 0 {
			t.Errorf("%s: %s", name, strings.Join(diffs, "\n"))
		}
	}
}

func TestDiff(t *testing.T) {
	got := Want()
	got.Items[1].Done = false
	got.ByID[7] = "eight"
	got.Audit = nil
	got.Strings = []string{}
	diffs := Diff(got, Want())
	want := []string{
		`Audit: got (*sample.Audit)(nil), want &sample.Audit{CreatedBy:"go"}`,
		`Strings: got []string{}, want []string{"a", "", "c"}`,
		`Items[1].Done: got false, want true`,
		`ByID[7]: got "eight", want "seven"`,
	}
	if strings.Join(diffs, "\n") != strings.Join(want, "\n") {
		t.Errorf("Diff:\n%s\nwant:\n%s", strings.Join(diffs, "\n"), strings.Join(want, "\n"))
	}

	empty := Everything{Strings: []string{}, ByName: map[string]Item{}}
	if diffs := Diff(empty, Everything{}); len(diffs) > 0 {
		t.Errorf("nil and empty differ: %v", diffs)
	}
}
