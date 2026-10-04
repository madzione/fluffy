package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files of TestGenerate")

// generateFixture copies testdata/generate into a project with the fluffy
// layer and returns its root.
func generateFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("testdata", "generate"))); err != nil {
		t.Fatal(err)
	}
	for _, golden := range []string{"api.g.dart", "fluffy_api.go"} {
		os.Remove(filepath.Join(root, golden))
	}
	if err := syncLayer(root, templateData{"demo"}); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestGenerate(t *testing.T) {
	root := generateFixture(t)
	files, a, err := generateFiles(root)
	if err != nil {
		t.Fatal(err)
	}

	var warnings []string
	for _, p := range a.problems {
		warnings = append(warnings, p.String())
	}
	for _, want := range []string{"skips Each: parameter fn", "skips Many: variadic"} {
		if !strings.Contains(strings.Join(warnings, "\n"), want) {
			t.Errorf("no warning %q in:\n%s", want, strings.Join(warnings, "\n"))
		}
	}

	for _, f := range files {
		golden := filepath.Join("testdata", "generate", filepath.Base(f.path))
		if *update {
			os.WriteFile(golden, f.content, 0o644)
		}
		want, _ := os.ReadFile(golden)
		if string(want) != string(f.content) {
			t.Errorf("%s differs from %s, run go test -run TestGenerate -update and check the diff:\n%s", f.path, golden, f.content)
		}
		if err := writeFile(filepath.Join(root, f.path), f.content); err != nil {
			t.Fatal(err)
		}
	}

	vetGenerated(t, root)

	// A second run is up to date.
	if err := checkGenerated(root); err != nil {
		t.Error(err)
	}
}

// vetGenerated checks that the Go code compiles on both kinds of targets.
func vetGenerated(t *testing.T, root string) {
	t.Helper()
	for _, env := range [][]string{{"CGO_ENABLED=1"}, {"GOOS=js", "GOARCH=wasm"}} {
		cmd := exec.Command("go", "vet", "./...")
		cmd.Dir = filepath.Join(root, "golang")
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("go vet with %v: %v\n%s", env, err, out)
		}
	}
}

// The generated Dart follows what encoding/json does in corner cases, and
// directives fluffy cannot see are reported.
func TestGenerateJSONRules(t *testing.T) {
	root := writeProject(t, map[string]string{"api.go": `package main

import "demo/golang/fluffy"

// Level has MarshalText with a pointer receiver, which encoding/json only
// uses for addressable values.
type Level struct{ v int }

func (l *Level) MarshalText() ([]byte, error) { return []byte("high"), nil }

type Box struct {
	N     *int   ` + "`json:\"n,string\"`" + `
	Inner Inner  ` + "`json:\"inner,omitempty\"`" + `
	Text  string ` + "`json:\"text,omitempty\"`" + `
	Level *Level ` + "`json:\"level\"`" + `
}

type Inner struct{ A int }

//fluffy:export
func GetLevel() Level { return Level{} }

//fluffy:export
func GetLevelPtr() *Level { return &Level{} }

//fluffy:export
func GetBox() Box { return Box{} }

//fluffy:dart
type UI = interface{ Show(msg string) }

//fluffy:export

func Detached() {}

var ui = newUI()

func main() { fluffy.Serve() }
`, "native.go": "//go:build !js\n\npackage main\n\n//fluffy:export\nfunc Native() {}\n"})

	files, a, err := generateFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	dart := string(files[0].content)
	for _, want := range []string{
		"Future<Level> getLevel() async =>",                                               // the value encodes as {}
		"Future<String?> getLevelPtr() async =>",                                          // MarshalText through the pointer
		"n: switch (m['n']) { final v0? => jsonDecode(v0 as String) as int, _ => null },", // ,string behind a pointer
		"'n': switch (n) { final v0? => jsonEncode(v0), _ => null },",
		"final Inner inner;", // omitempty never leaves a struct out
		"final String? text;",
		"final String? level;",
		"abstract class UI {",
	} {
		if !strings.Contains(dart, want) {
			t.Errorf("missing %q in:\n%s", want, dart)
		}
	}
	if strings.Contains(dart, "detached") || strings.Contains(dart, "native") {
		t.Errorf("Dart has functions fluffy does not export:\n%s", dart)
	}

	var warnings []string
	for _, p := range a.problems {
		warnings = append(warnings, p.String())
	}
	for _, want := range []string{
		"golang/api.go:32:1: warning: //fluffy:export is not attached to a function",
		"golang/native.go:5: warning: native.go is not part of the web build (build constraints)",
	} {
		if !strings.Contains(strings.Join(warnings, "\n"), want) {
			t.Errorf("missing warning %q in:\n%s", want, strings.Join(warnings, "\n"))
		}
	}

	for _, f := range files {
		if err := writeFile(filepath.Join(root, f.path), f.content); err != nil {
			t.Fatal(err)
		}
	}
	vetGenerated(t, root)
}

// writeProject writes a project with the fluffy layer and the given files in
// golang/.
func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module demo/golang\n\ngo 1.21\n"
	for name, content := range files {
		if err := writeFile(filepath.Join(root, "golang", name), []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := syncLayer(root, templateData{"demo"}); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestGenerateConflicts(t *testing.T) {
	root := writeProject(t, map[string]string{"api.go": `package main

import "demo/golang/fluffy"

type T struct{}

//fluffy:export
func (T) Method() {}

//fluffy:dart
type NotAnInterface struct{}

//fluffy:dart
type Callbacks interface {
	Pick(options ...string) (string, error)
}

//fluffy:typo
func main() { fluffy.Serve() }

func init() {
	fluffy.Handle("dup", func(x int) (int, error) { return x, nil })
	fluffy.Handle("dup", func(x int) (int, error) { return x, nil })
	fluffy.Emit("event", 1)
	fluffy.Emit("event", "text")
	fluffy.On("both", func(x int) {})
	fluffy.Emit("both", "text")
	fluffy.On("my-name", func(x int) {})
	fluffy.On("my_name", func(x int) {})
	fluffy.Handle("fn", func(f func()) (int, error) { return 0, nil })
	channel := "dynamic"
	fluffy.Emit(channel, 1)
}

type Stamp struct{ v int }

func (*Stamp) MarshalJSON() ([]byte, error) { return []byte("1"), nil }

type Event struct{ At Stamp }

//fluffy:export
func LastEvent() Event { return Event{} }

//fluffy:export
func Events() []Event { return nil }
`})
	_, a, err := generateFiles(root)
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{
		`api.go:8:10: //fluffy:export on the method Method`,
		`//fluffy:dart on NotAnInterface, which is not an interface`,
		`//fluffy:dart Callbacks.Pick: variadic functions are not supported`,
		`channel "dup" is registered twice, first at golang/api.go:22:2`,
		`channel "event" is sent with string here and with int at golang/api.go:24:2`,
		`channel "both" is sent from Go and handled in Go at golang/api.go:26:2 with different types`,
		`the Dart name myName is already used for golang/api.go:28:2`,
		`func() cannot be sent as JSON`,
		`Event is sent both as a value and through a pointer (or in a slice), which encoding/json encodes differently because Stamp.MarshalJSON has a pointer receiver`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing error %q in:\n%v", want, err)
		}
	}
	var warnings []string
	for _, p := range a.problems {
		if p.warning {
			warnings = append(warnings, p.String())
		}
	}
	for _, want := range []string{
		"unknown directive //fluffy:typo",
		"fluffy.Emit with a channel that is not a constant",
	} {
		if !strings.Contains(strings.Join(warnings, "\n"), want) {
			t.Errorf("missing warning %q in:\n%s", want, strings.Join(warnings, "\n"))
		}
	}
}

// The Go code has to compile for generate to work, except for the
// constructors it generates itself.
func TestGenerateNeedsCompilingCode(t *testing.T) {
	root := writeProject(t, map[string]string{"main.go": `package main

//fluffy:dart
type UI interface{ Show(text string) }

var ui = newUI()

func main() { undefinedFunction() }
`})
	_, _, err := generateFiles(root)
	if err == nil || !strings.Contains(err.Error(), "undefined: undefinedFunction") || strings.Contains(err.Error(), "newUI") {
		t.Errorf("err = %v", err)
	}
}

func TestDartNames(t *testing.T) {
	cases := map[string]string{
		"Add":         "add",
		"UserID":      "userID",
		"ID":          "id",
		"HTTPServer":  "httpServer",
		"CPUs":        "cpus",
		"IDs":         "ids",
		"URLsByHost":  "urlsByHost",
		"hello_world": "helloWorld",
		"my-channel":  "myChannel",
		"2fa":         "n2fa",
	}
	for in, want := range cases {
		if got := lowerCamel(in); got != want {
			t.Errorf("lowerCamel(%q) = %q, want %q", in, got, want)
		}
	}
	if got := upperCamel("todo_list"); got != "TodoList" {
		t.Errorf("upperCamel = %q", got)
	}
	if got := dartIdent("new"); got != "new_" {
		t.Errorf("dartIdent(new) = %q", got)
	}
	if got := parens("(a) + (b)"); got != "((a) + (b))" {
		t.Errorf("parens = %q", got)
	}
}
