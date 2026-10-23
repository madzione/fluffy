package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// listedPackage holds the fields of go list -json that collectDeps reads.
type listedPackage struct {
	Dir    string
	Module *struct {
		Main    bool
		Dir     string
		GoMod   string
		Replace *struct{ Version string }
	}
	GoFiles, CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles []string
	SwigFiles, SwigCXXFiles, SysoFiles, EmbedFiles                      []string
	// Files left out by build constraints, e.g. store_js.go in a native
	// build: fluffy generate reads the package as GOOS=js, so the Dart API
	// can depend on them.
	IgnoredGoFiles, IgnoredOtherFiles []string
}

// collectDeps adds the inputs of a build of golang/ with env to b.deps, for
// the cache of the Flutter build hook: it builds again only when one of them
// changed. Only packages of the user's modules (main module, go.work, local
// replace) are listed, the module cache is pinned by go.mod and go.sum.
func (b builder) collectDeps(env []string) error {
	dir := filepath.Join(b.root, "golang")
	cmd := exec.Command("go", "list", "-deps", "-json=Dir,Module,GoFiles,CgoFiles,CFiles,CXXFiles,MFiles,HFiles,FFiles,SFiles,"+
		"SwigFiles,SwigCXXFiles,SysoFiles,EmbedFiles,IgnoredGoFiles,IgnoredOtherFiles", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go list: %w", err)
	}

	decoder := json.NewDecoder(&stdout)
	for {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("go list: %w", err)
		}
		m := pkg.Module
		if m == nil || !(m.Main || m.Replace != nil && m.Replace.Version == "") {
			continue
		}
		// The hook runner hashes a directory by the names in it, which
		// notices a file added to the package.
		b.deps[pkg.Dir+string(filepath.Separator)] = true
		for _, files := range [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.FFiles,
			pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles, pkg.SysoFiles, pkg.EmbedFiles, pkg.IgnoredGoFiles, pkg.IgnoredOtherFiles} {
			for _, file := range files {
				b.deps[filepath.Join(pkg.Dir, file)] = true
			}
		}
		if m.GoMod != "" {
			b.deps[m.GoMod] = true
			b.deps[filepath.Join(filepath.Dir(m.GoMod), "go.sum")] = true
		}
	}

	// A go.work created in golang/ later changes the names in golang/.
	gowork := exec.Command("go", "env", "GOWORK")
	gowork.Dir = dir
	out, err := gowork.Output()
	if err != nil {
		return fmt.Errorf("go env GOWORK: %w", err)
	}
	if work := strings.TrimSpace(string(out)); work != "" && work != "off" {
		b.deps[work] = true
		b.deps[work+".sum"] = true
	}

	// A new Go release or a fluffy that builds differently builds again.
	for _, find := range []func() (string, error){
		func() (string, error) { return exec.LookPath("go") },
		os.Executable,
	} {
		if exe, err := find(); err == nil {
			if real, err := filepath.EvalSymlinks(exe); err == nil {
				exe = real
			}
			if abs, err := filepath.Abs(exe); err == nil {
				b.deps[abs] = true
			}
		}
	}
	return nil
}

// writeDeps writes the collected inputs to file, one absolute path per line,
// directories with a trailing separator. Missing files are left out: the
// hook runner takes them for files changed during the build and asks for
// another one.
func (b builder) writeDeps(file string) error {
	lines := make([]string, 0, len(b.deps))
	for dep := range b.deps {
		if _, err := os.Stat(dep); err == nil {
			lines = append(lines, dep)
		}
	}
	sort.Strings(lines)
	return writeFile(file, []byte(strings.Join(lines, "\n")+"\n"))
}
