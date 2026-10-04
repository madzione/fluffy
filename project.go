package main

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

//go:embed _templates
var templates embed.FS

// create runs flutter create and init. With FVM installed the project is
// created with, and pinned to, the version from --fvm, the .fvmrc of the
// working directory or a parent, or the FVM global version, in that order.
func create(args []string) error {
	patchGtk, args := boolFlag(args, "--patch-gtk")
	version, args, err := fvmFlag(args)
	if err != nil {
		return err
	}
	if len(args) == 0 || strings.HasPrefix(args[len(args)-1], "-") {
		return errors.New("usage: fluffy create [--fvm <version>] [--patch-gtk] [flutter create flags] <dir>")
	}
	dir := args[len(args)-1]

	if _, lookErr := exec.LookPath("fvm"); lookErr != nil {
		if version != "" {
			return errors.New("--fvm needs fvm on PATH")
		}
		if err := flutter(".", append([]string{"create"}, args...)...); err != nil {
			return err
		}
		return initProject(dir, true, patchGtk)
	}

	if version == "" {
		version = fvmVersion(".")
	}
	if version == "" {
		fmt.Println("fluffy: fvm has no pinned or global Flutter version, using flutter from PATH")
		if err := flutter(".", append([]string{"create"}, args...)...); err != nil {
			return err
		}
		return initProject(dir, true, patchGtk)
	}

	fmt.Printf("fluffy: creating the project with Flutter %s (fvm)\n", version)
	if err := fvm(".", append([]string{"spawn", version, "create"}, args...)...); err != nil {
		return err
	}
	// fvm use pins the nearest directory with a .fvmrc, which is a parent
	// when the version came from there. Writing the project's own .fvmrc
	// first makes fvm use set up the new project instead.
	fvmrc := fmt.Sprintf("{\n  \"flutter\": %q\n}\n", version)
	if err := os.WriteFile(filepath.Join(dir, ".fvmrc"), []byte(fvmrc), 0o644); err != nil {
		return err
	}
	// pub get runs later through flutter pub add ffi.
	if err := fvm(dir, "use", version, "--skip-pub-get", "--fvm-skip-input"); err != nil {
		return err
	}
	return initProject(dir, true, patchGtk)
}

// boolFlag removes every name from args and reports if there was one.
func boolFlag(args []string, name string) (bool, []string) {
	found := false
	rest := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == name {
			found = true
			continue
		}
		rest = append(rest, arg)
	}
	return found, rest
}

// fvmFlag removes --fvm <version> / --fvm=<version> from args.
func fvmFlag(args []string) (string, []string, error) {
	var version string
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--fvm":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return "", nil, errors.New("--fvm needs a Flutter version, e.g. --fvm 3.24.0 or --fvm stable")
			}
			version = args[i+1]
			i++
		case strings.HasPrefix(arg, "--fvm="):
			version = strings.TrimPrefix(arg, "--fvm=")
		default:
			rest = append(rest, arg)
		}
	}
	return version, rest, nil
}

// initProject adds the Go library, the Dart side and the build hooks to the
// project in dir. demo replaces the counter app of a project flutter create
// just made with an app that uses Go. patchGtk also applies the optional
// GTK patch to the Linux runner.
func initProject(dir string, demo, patchGtk bool) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	pubspec, err := os.ReadFile(filepath.Join(root, "pubspec.yaml"))
	if err != nil {
		return fmt.Errorf("no pubspec.yaml in %s, fluffy init has to run in a Flutter project", root)
	}
	pkg := packageName(pubspec)
	if pkg == "" {
		return errors.New("pubspec.yaml has no name")
	}

	data := templateData{pkg}
	if err := scaffold(root, "_templates/golang", "golang", data, false); err != nil {
		return err
	}
	if err := scaffold(root, "_templates/dart", filepath.Join("lib", "golang"), data, false); err != nil {
		return err
	}
	if err := syncLayer(root, data); err != nil {
		return err
	}
	if demo {
		if err := scaffold(root, "_templates/app", ".", data, true); err != nil {
			return err
		}
	}
	if err := applyHooks(root, patchGtk); err != nil {
		return err
	}
	if _, err := generate(root, true); err != nil {
		fmt.Fprintf(os.Stderr, "fluffy: %v\nfluffy: fix the Go code and run fluffy generate\n", err)
	}

	if !hasDependency(pubspec, "ffi") {
		if err := flutter(root, "pub", "add", "ffi"); err != nil {
			fmt.Fprintln(os.Stderr, "fluffy: could not add the ffi package, run \"flutter pub add ffi\" yourself")
		}
	}

	fmt.Printf(`
Go is set up. Call it from Dart with:

  import 'package:%s/golang/golang.dart' as golang;

  final sum = await golang.add(1, 2);

Mark Go functions in golang/ with //fluffy:export and run "fluffy generate"
(fluffy run does it for you) to call them from Dart. The generated Dart API
is lib/golang/api.g.dart.

Native builds need the fluffy command on PATH. For web use "fluffy run".
`, pkg)
	return nil
}

type templateData struct{ Package string }

// The communication layer belongs to fluffy, not to the user: init and
// update replace these directories with the templates of this fluffy
// version, so the Go and Dart side always speak the same protocol.
var layer = []struct{ src, dst string }{
	{"_templates/layer/golang", "golang/fluffy"},
	{"_templates/layer/dart", "lib/golang/src"},
}

// updateProject brings an initialized project up to this fluffy version:
// the communication layer, the build hooks and the generated API. The
// user's code is kept.
func updateProject(root string, patchGtk bool) error {
	pubspec, err := os.ReadFile(filepath.Join(root, "pubspec.yaml"))
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "golang", "go.mod")); err != nil {
		return fmt.Errorf("no golang/go.mod in %s, run fluffy init first", root)
	}
	if err := syncLayer(root, templateData{packageName(pubspec)}); err != nil {
		return err
	}
	if err := applyHooks(root, patchGtk); err != nil {
		return err
	}
	if _, err := generate(root, true); err != nil {
		fmt.Fprintf(os.Stderr, "fluffy: %v\nfluffy: fix the Go code and run fluffy generate\n", err)
	}
	return nil
}

// syncLayer writes the communication layer into the project, replacing
// changed files and removing files the templates no longer have.
func syncLayer(root string, data templateData) error {
	removed := false
	defer func() {
		if removed {
			fmt.Println("fluffy: golang/fluffy/ and lib/golang/src/ belong to fluffy, files that are not part of it are removed")
		}
	}()
	for _, dir := range layer {
		files, err := renderTemplates(dir.src, data)
		if err != nil {
			return err
		}
		// The layer's own tests run in fluffy's test suite, not in the
		// user's.
		for rel := range files {
			if strings.HasSuffix(rel, "_test.go") {
				delete(files, rel)
			}
		}
		for _, rel := range sortedKeys(files) {
			content := files[rel]
			target := filepath.Join(root, dir.dst, filepath.FromSlash(rel))
			display := path.Join(dir.dst, rel)
			old, err := os.ReadFile(target)
			switch {
			case err == nil && bytes.Equal(old, content):
				fmt.Printf("  ok       %s\n", display)
				continue
			case err == nil:
				fmt.Printf("  updated  %s\n", display)
			default:
				fmt.Printf("  created  %s\n", display)
			}
			if err := writeFile(target, content); err != nil {
				return err
			}
		}

		base := filepath.Join(root, dir.dst)
		err = filepath.WalkDir(base, func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			rel, err := filepath.Rel(base, name)
			if err != nil {
				return err
			}
			if _, ok := files[filepath.ToSlash(rel)]; ok {
				return nil
			}
			fmt.Printf("  removed  %s\n", path.Join(dir.dst, filepath.ToSlash(rel)))
			removed = true
			return os.Remove(name)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// scaffold copies the embedded template directory src to dst in the project.
// Unless overwrite is set existing files are kept, they belong to the user
// after the first init.
func scaffold(root, src, dst string, data templateData, overwrite bool) error {
	files, err := renderTemplates(src, data)
	if err != nil {
		return err
	}
	for _, rel := range sortedKeys(files) {
		target := filepath.Join(root, dst, filepath.FromSlash(rel))
		display := path.Join(filepath.ToSlash(dst), rel)
		action := "created"
		if _, err := os.Stat(target); err == nil {
			if !overwrite {
				fmt.Printf("  kept     %s\n", display)
				continue
			}
			action = "replaced"
		}
		fmt.Printf("  %-8s %s\n", action, display)
		if err := writeFile(target, files[rel]); err != nil {
			return err
		}
	}
	return nil
}

// renderTemplates returns the files of the embedded template directory src
// by their slash separated path relative to src. Files ending in .tmpl are
// rendered with data and lose the suffix.
func renderTemplates(src string, data templateData) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(templates, src, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := templates.ReadFile(name)
		if err != nil {
			return err
		}

		rel := strings.TrimPrefix(name, src+"/")
		if strings.HasSuffix(rel, ".tmpl") {
			rel = strings.TrimSuffix(rel, ".tmpl")
			tmpl, err := template.New(path.Base(name)).Parse(string(content))
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, data); err != nil {
				return err
			}
			content = buf.Bytes()
		}
		files[rel] = content
		return nil
	})
	return files, err
}

func sortedKeys(files map[string][]byte) []string {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeFile(name string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return os.WriteFile(name, content, 0o644)
}

// hasDependency reports if pubspec has name under dependencies, not only
// under dev_dependencies or dependency_overrides.
func hasDependency(pubspec []byte, name string) bool {
	inDependencies := false
	for _, line := range strings.Split(string(pubspec), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
		case line[0] != ' ' && line[0] != '\t':
			inDependencies = strings.HasPrefix(trimmed, "dependencies:")
		case inDependencies && strings.HasPrefix(trimmed, name+":"):
			return true
		}
	}
	return false
}

func packageName(pubspec []byte) string {
	match := regexp.MustCompile(`(?m)^name:\s*["']?([A-Za-z0-9_]+)`).FindSubmatch(pubspec)
	if match == nil {
		return ""
	}
	return string(match[1])
}

// findProjectRoot walks up from the working directory to the nearest
// directory with a pubspec.yaml, so build hooks can call fluffy from anywhere
// inside the project.
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "pubspec.yaml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("not inside a Flutter project (no pubspec.yaml found)")
		}
		dir = parent
	}
}
