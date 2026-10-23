package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The build hook builds again only when a file of --deps changed, so every
// input of the build has to be listed: packages of the main module and of
// local replace and go.work modules, embedded files, files of other build
// targets, and directories, which tell about added files.
func TestDeps(t *testing.T) {
	root := generateFixture(t)
	golang := filepath.Join(root, "golang")
	for name, content := range map[string]string{
		"embed.go":           "package main\n\nimport _ \"embed\"\n\n//go:embed assets/version.txt\nvar version string\n",
		"assets/version.txt": "1.0.0\n",
		"web_js.go":          "package main\n",
	} {
		if err := writeFile(filepath.Join(golang, name), []byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	deps := func() []string {
		t.Helper()
		b := builder{root: root, deps: map[string]bool{}}
		if err := b.collectDeps([]string{"CGO_ENABLED=1"}); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "deps.txt")
		if err := b.writeDeps(file); err != nil {
			t.Fatal(err)
		}
		content, _ := os.ReadFile(file)
		var rel []string
		for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
			if !filepath.IsAbs(line) {
				t.Errorf("relative path %q", line)
			}
			dir := strings.HasSuffix(line, string(filepath.Separator))
			if r, err := filepath.Rel(root, line); err == nil && !strings.HasPrefix(r, "..") {
				line = filepath.ToSlash(r)
			} else {
				line = "outside:" + filepath.Base(line)
			}
			if dir {
				line += "/"
			}
			rel = append(rel, line)
		}
		return rel
	}

	got := deps()
	for _, want := range []string{
		"golang/", "golang/go.mod", "golang/main.go", "golang/api.go", "golang/embed.go",
		"golang/assets/version.txt", // go:embed
		"golang/web_js.go",          // read by fluffy generate
		"golang/fluffy/", "golang/fluffy/bridge.go",
		"todo/", "todo/go.mod", "todo/todo.go", // local replace
		"outside:go", "outside:" + filepath.Base(os.Args[0]), // the toolchain and fluffy itself
	} {
		if !slices.Contains(got, want) {
			t.Errorf("deps lack %s:\n%s", want, strings.Join(got, "\n"))
		}
	}
	// The hook runner takes a missing file for one changed during the build.
	for _, missing := range []string{"golang/go.sum", "golang/go.work"} {
		if slices.Contains(got, missing) {
			t.Errorf("deps list the missing %s", missing)
		}
	}

	work := "go 1.21\n\nuse (\n\t.\n\t../todo\n)\n"
	if err := os.WriteFile(filepath.Join(golang, "go.work"), []byte(work), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "")
	got = deps()
	for _, want := range []string{"golang/go.work", "todo/todo.go"} {
		if !slices.Contains(got, want) {
			t.Errorf("with go.work, deps lack %s:\n%s", want, strings.Join(got, "\n"))
		}
	}
}
