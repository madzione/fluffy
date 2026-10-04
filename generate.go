package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The files fluffy generate owns. They are overwritten on every run.
const (
	generatedGo   = "fluffy_api.go" // in golang/
	generatedDart = "api.g.dart"    // in lib/golang/
)

func generateCommand(args []string) error {
	flags := flag.NewFlagSet("generate", flag.ContinueOnError)
	check := flags.Bool("check", false, "fail if the generated files are out of date, without writing them")
	watch := flags.Bool("watch", false, "generate again whenever the Go code changes")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q, usage: fluffy generate [--check | --watch]", flags.Arg(0))
	}
	root, err := findProjectRoot()
	if err != nil {
		return err
	}
	switch {
	case *watch:
		return watchGenerate(root)
	case *check:
		return checkGenerated(root)
	}
	_, err = generate(root, true)
	return err
}

type generatedFile struct {
	path    string // relative to the project root
	content []byte // nil removes the file
}

// generateFiles analyzes the Go code and returns the generated files. The
// api is returned even on errors when the packages could be loaded.
func generateFiles(root string) ([]generatedFile, *api, error) {
	a, err := analyze(root)
	if err != nil {
		return nil, a, err
	}
	files := []generatedFile{
		{filepath.Join("lib", "golang", generatedDart), a.generateDart()},
		{filepath.Join("golang", generatedGo), a.generateGo()},
	}

	var errs []string
	sort.SliceStable(a.problems, func(i, j int) bool { return a.problems[i].warning && !a.problems[j].warning })
	for _, p := range a.problems {
		if p.warning {
			fmt.Fprintf(os.Stderr, "fluffy: %s\n", p)
		} else {
			errs = append(errs, p.String())
		}
	}
	if len(errs) > 0 {
		return nil, a, fmt.Errorf("generate failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return files, a, nil
}

// generate writes the generated files. verbose also lists the unchanged
// ones, otherwise only changes are printed.
func generate(root string, verbose bool) (*api, error) {
	files, a, err := generateFiles(root)
	if err != nil {
		return a, err
	}
	for _, f := range files {
		target := filepath.Join(root, f.path)
		display := filepath.ToSlash(f.path)
		old, readErr := os.ReadFile(target)
		switch {
		case f.content == nil && readErr != nil:
			continue
		case f.content == nil:
			fmt.Printf("  removed  %s\n", display)
			if err := os.Remove(target); err != nil {
				return a, err
			}
			continue
		case readErr == nil && bytes.Equal(old, f.content):
			if verbose {
				fmt.Printf("  ok       %s\n", display)
			}
			continue
		case readErr == nil:
			fmt.Printf("  updated  %s\n", display)
		default:
			fmt.Printf("  created  %s\n", display)
		}
		if err := writeFile(target, f.content); err != nil {
			return a, err
		}
	}

	userAPI, err := os.ReadFile(filepath.Join(root, "lib", "golang", "api.dart"))
	if err == nil && !bytes.Contains(userAPI, []byte(generatedDart)) {
		fmt.Fprintf(os.Stderr, "fluffy: lib/golang/api.dart does not export the generated API, add: export '%s';\n", generatedDart)
	}
	return a, nil
}

// refreshGeneratedGo writes golang/fluffy_api.go again if it is out of date
// and reports if it did. fluffy lib runs it when go build fails: a stale
// file that registers a removed function breaks the build in code the user
// never wrote. The Dart file is left alone, flutter may be compiling it.
func refreshGeneratedGo(root string) bool {
	files, _, err := generateFiles(root)
	if err != nil {
		return false
	}
	for _, f := range files {
		if filepath.Base(f.path) != generatedGo {
			continue
		}
		target := filepath.Join(root, f.path)
		old, readErr := os.ReadFile(target)
		switch {
		case f.content == nil && readErr != nil, f.content != nil && readErr == nil && bytes.Equal(old, f.content):
			return false
		case f.content == nil:
			return os.Remove(target) == nil
		default:
			return writeFile(target, f.content) == nil
		}
	}
	return false
}

// checkGenerated fails if a generated file differs from what generate would
// write, for CI.
func checkGenerated(root string) error {
	files, _, err := generateFiles(root)
	if err != nil {
		return err
	}
	var stale []string
	for _, f := range files {
		old, err := os.ReadFile(filepath.Join(root, f.path))
		exists := err == nil
		if exists != (f.content != nil) || exists && !bytes.Equal(old, f.content) {
			stale = append(stale, filepath.ToSlash(f.path))
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("out of date: %s, run fluffy generate", strings.Join(stale, ", "))
	}
	fmt.Println("fluffy: generated files are up to date")
	return nil
}

// generateBefore runs generate before a flutter command, so Dart compiles
// against the current Go API. Projects without golang/ are left alone.
func generateBefore(root string) error {
	if _, err := os.Stat(filepath.Join(root, "golang", "go.mod")); err != nil {
		return nil
	}
	_, err := generate(root, false)
	return err
}

// watchGenerate generates again whenever a Go file of the scanned packages,
// a go.mod or a go.work changes. Errors are printed, watching goes on.
func watchGenerate(root string) error {
	dirs := []string{filepath.Join(root, "golang")}
	for {
		a, err := generate(root, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fluffy: %v\n", err)
		}
		if a != nil && len(a.dirs) > 0 {
			dirs = a.dirs
		}
		fmt.Printf("fluffy: watching %d directories for changes, Ctrl+C to stop\n", len(dirs))

		last := fingerprint(dirs)
		for {
			time.Sleep(500 * time.Millisecond)
			if now := fingerprint(dirs); now != last {
				// Let editors finish writing.
				time.Sleep(100 * time.Millisecond)
				break
			}
		}
	}
}

// fingerprint hashes the names, sizes and modification times of the files
// that affect the generated code.
func fingerprint(dirs []string) uint64 {
	h := fnv.New64a()
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			fmt.Fprintf(h, "%s: %v\n", dir, err)
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if name == generatedGo || !(strings.HasSuffix(name, ".go") || name == "go.mod" || name == "go.work") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			fmt.Fprintf(h, "%s %d %d\n", filepath.Join(dir, name), info.Size(), info.ModTime().UnixNano())
		}
	}
	return h.Sum64()
}
