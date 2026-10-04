package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

var goTargets = []string{"linux", "windows", "macos", "android", "ios", "web"}

func isGoTarget(target string) bool {
	return slices.Contains(goTargets, target)
}

// builder compiles the project's golang/ directory. Every build goes through
// a temporary directory so only the library itself (no cgo header) lands in
// the output directory.
type builder struct {
	root       string   // Flutter project root
	out        string   // output directory, empty for the target's default
	archs      []string // GOARCH values, empty for the target's default
	strip      bool     // leave out the symbol table and DWARF
	ndkVersion string   // Android NDK in the SDK, empty for the newest
}

// buildCommand is fluffy build: flutter build after generating the API. Web
// has no build hook, so its Go library is built first.
func buildCommand(args []string) error {
	if len(args) > 0 && fromOldHook(args) {
		fmt.Fprintf(os.Stderr, "fluffy: the build hooks of this project call \"fluffy build %s\", which builds the whole app now. "+
			"Building only the Go library, run fluffy update to switch the hooks to \"fluffy lib %s\".\n", args[0], args[0])
		return libCommand(args)
	}
	if root, err := findProjectRoot(); err == nil {
		if err := generateBefore(root); err != nil {
			return err
		}
		if len(args) > 0 && args[0] == "web" {
			// flutter build web is a release build unless told otherwise.
			release := !slices.Contains(args, "--debug") && !slices.Contains(args, "--profile")
			if err := buildWeb(root, release); err != nil {
				return err
			}
		}
	}
	return flutter(".", append([]string{"build"}, args...)...)
}

// fromOldHook reports if "fluffy build <target>" comes from a build hook of
// an older fluffy, which built only the Go library with it: for android
// (flutter has no such build target), with flags of fluffy lib, or inside a
// flutter command fluffy started. Passing it on to flutter build would
// build the app again from inside its own build.
func fromOldHook(args []string) bool {
	if !isGoTarget(args[0]) || args[0] == "web" { // web never had a hook
		return false
	}
	if args[0] == "android" || os.Getenv(insideFlutter) != "" {
		return true
	}
	for _, arg := range args[1:] {
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if strings.HasPrefix(arg, "-") && slices.Contains([]string{"o", "arch", "sdk", "android-api"}, name) {
			return true
		}
	}
	return false
}

// buildWeb builds web/golang.wasm when the project has golang/.
func buildWeb(root string, strip bool) error {
	if _, err := os.Stat(filepath.Join(root, "golang", "go.mod")); err != nil {
		return nil
	}
	return builder{root: root, strip: strip}.web()
}

// libCommand is fluffy lib <target>: it builds only the Go library.
func libCommand(args []string) error {
	if len(args) == 0 || !isGoTarget(args[0]) {
		return fmt.Errorf("usage: fluffy lib <%s> [flags]", strings.Join(goTargets, "|"))
	}
	target := args[0]
	flags := flag.NewFlagSet("lib "+target, flag.ContinueOnError)
	out := flags.String("o", "", "output directory")
	arch := flags.String("arch", "", "architectures, e.g. \"arm64 x86_64\"")
	sdk := flags.String("sdk", "iphoneos", "iOS SDK: iphoneos or iphonesimulator")
	api := flags.Int("android-api", 21, "Android API level to compile against")
	ndkVersion := flags.String("ndk-version", "", "Android NDK version in the SDK to use (default: the newest)")
	strip := flags.Bool("strip", false, "leave out the symbol table and debug info (-ldflags=\"-s -w\")")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	root, err := findProjectRoot()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "golang")); err != nil {
		return fmt.Errorf("no golang/ directory in %s, run fluffy init first", root)
	}

	b := builder{root: root, strip: *strip, ndkVersion: *ndkVersion}
	if *out != "" {
		if b.out, err = filepath.Abs(*out); err != nil {
			return err
		}
	}
	if b.archs, err = parseArchs(*arch); err != nil {
		return err
	}

	build := func() error {
		switch target {
		case "linux":
			return b.desktop("linux", "libgolang.so")
		case "windows":
			return b.desktop("windows", "golang.dll")
		case "macos":
			return b.macos()
		case "android":
			return b.android(*api)
		case "ios":
			return b.ios(*sdk)
		case "web":
			return b.web()
		}
		return fmt.Errorf("unknown target %q", target)
	}
	err = build()
	// The build hooks don't generate, so golang/fluffy_api.go may still
	// register a function that was removed or renamed since.
	if err != nil && refreshGeneratedGo(root) {
		fmt.Fprintln(os.Stderr, "fluffy: golang/fluffy_api.go was out of date and has been generated again, building again. "+
			"Run fluffy generate to update lib/golang/api.g.dart too.")
		err = build()
	}
	return err
}

// parseArchs turns a space or comma separated list of Go, Xcode or Android
// architecture names into GOARCH values.
func parseArchs(list string) ([]string, error) {
	var archs []string
	for _, name := range strings.FieldsFunc(list, func(r rune) bool { return r == ' ' || r == ',' }) {
		switch name {
		case "amd64", "x86_64", "x64":
			archs = append(archs, "amd64")
		case "arm64", "aarch64", "arm64-v8a":
			archs = append(archs, "arm64")
		case "arm", "armv7", "armeabi-v7a":
			archs = append(archs, "arm")
		case "386", "x86", "i386":
			archs = append(archs, "386")
		default:
			return nil, fmt.Errorf("unknown architecture %q", name)
		}
	}
	return archs, nil
}

func (b builder) outDir(def string) string {
	if b.out != "" {
		return b.out
	}
	return filepath.Join(b.root, def)
}

func (b builder) archsOr(def ...string) []string {
	if len(b.archs) > 0 {
		return b.archs
	}
	return def
}

func (b builder) desktop(goos, lib string) error {
	archs := b.archsOr(runtime.GOARCH)
	if len(archs) != 1 {
		return fmt.Errorf("%s builds exactly one architecture, got %v", goos, archs)
	}
	arch := archs[0]

	env := []string{"GOOS=" + goos, "GOARCH=" + arch, "CGO_ENABLED=1"}
	if goos == "windows" && runtime.GOOS != "windows" && os.Getenv("CC") == "" {
		// Cross-compiling needs a MinGW toolchain for cgo.
		cc := map[string]string{"amd64": "x86_64-w64-mingw32-gcc", "arm64": "aarch64-w64-mingw32-gcc", "386": "i686-w64-mingw32-gcc"}[arch]
		env = append(env, "CC="+cc)
	}

	return b.buildInto(env, "c-shared", filepath.Join(b.outDir(filepath.Join("build", "fluffy", goos)), lib))
}

func (b builder) macos() error {
	archs := b.archsOr(runtime.GOARCH)
	var parts []string
	tmp, err := os.MkdirTemp("", "fluffy-macos")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	for _, arch := range archs {
		part := filepath.Join(tmp, arch, "libgolang.dylib")
		env := []string{"GOOS=darwin", "GOARCH=" + arch, "CGO_ENABLED=1"}
		if err := b.goBuild(env, "c-shared", part); err != nil {
			return err
		}
		parts = append(parts, part)
	}

	return installFat(parts, filepath.Join(b.outDir(filepath.Join("build", "fluffy", "macos")), "libgolang.dylib"))
}

func (b builder) ios(sdk string) error {
	if sdk != "iphoneos" && sdk != "iphonesimulator" {
		return fmt.Errorf("unknown iOS SDK %q, use iphoneos or iphonesimulator", sdk)
	}
	sdkPath, err := output("xcrun", "--sdk", sdk, "--show-sdk-path")
	if err != nil {
		return fmt.Errorf("iOS builds need Xcode: %w", err)
	}
	clang, err := output("xcrun", "--sdk", sdk, "--find", "clang")
	if err != nil {
		return err
	}
	minVersion := os.Getenv("IPHONEOS_DEPLOYMENT_TARGET")
	if minVersion == "" {
		minVersion = "13.0"
	}

	tmp, err := os.MkdirTemp("", "fluffy-ios")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	var parts []string
	for _, arch := range b.archsOr("arm64") {
		appleArch := map[string]string{"arm64": "arm64", "amd64": "x86_64"}[arch]
		if appleArch == "" {
			return fmt.Errorf("unsupported iOS architecture %q", arch)
		}
		triple := appleArch + "-apple-ios" + minVersion
		if sdk == "iphonesimulator" {
			triple += "-simulator"
		}
		flags := "-isysroot " + sdkPath + " -target " + triple

		part := filepath.Join(tmp, arch, "libgolang.a")
		env := []string{"GOOS=ios", "GOARCH=" + arch, "CGO_ENABLED=1", "CC=" + clang, "CGO_CFLAGS=" + flags, "CGO_LDFLAGS=" + flags}
		if err := b.goBuild(env, "c-archive", part); err != nil {
			return err
		}
		parts = append(parts, part)
	}

	return installFat(parts, filepath.Join(b.outDir(filepath.Join("build", "fluffy", "ios", sdk)), "libgolang.a"))
}

var androidABIs = map[string]struct{ abi, triple string }{
	"arm64": {"arm64-v8a", "aarch64-linux-android"},
	"arm":   {"armeabi-v7a", "armv7a-linux-androideabi"},
	"amd64": {"x86_64", "x86_64-linux-android"},
	"386":   {"x86", "i686-linux-android"},
}

func (b builder) android(api int) error {
	ndk, err := findNDK(b.root, b.ndkVersion)
	if err != nil {
		return err
	}
	host := map[string]string{"windows": "windows-x86_64", "darwin": "darwin-x86_64"}[runtime.GOOS]
	if host == "" {
		host = "linux-x86_64"
	}
	jniLibs := b.outDir(filepath.Join("android", "app", "src", "main", "jniLibs"))

	for _, arch := range b.archsOr("arm64", "arm", "amd64") {
		target, ok := androidABIs[arch]
		if !ok {
			return fmt.Errorf("unsupported Android architecture %q", arch)
		}
		cc := filepath.Join(ndk, "toolchains", "llvm", "prebuilt", host, "bin", fmt.Sprintf("%s%d-clang", target.triple, api))
		if runtime.GOOS == "windows" {
			cc += ".cmd"
		}

		env := []string{"GOOS=android", "GOARCH=" + arch, "CGO_ENABLED=1", "CC=" + cc}
		if arch == "arm" {
			env = append(env, "GOARM=7")
		}
		if err := b.buildInto(env, "c-shared", filepath.Join(jniLibs, target.abi, "libgolang.so")); err != nil {
			return err
		}
	}
	return nil
}

func (b builder) web() error {
	out := b.outDir("web")
	env := []string{"GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0"}
	if err := b.buildInto(env, "", filepath.Join(out, "golang.wasm")); err != nil {
		return err
	}

	// wasm_exec.js has to come from the same Go release that compiled the module.
	goroot, err := output("go", "env", "GOROOT")
	if err != nil {
		return err
	}
	for _, dir := range []string{"lib", "misc"} {
		src := filepath.Join(goroot, dir, "wasm", "wasm_exec.js")
		if _, err := os.Stat(src); err == nil {
			return copyFile(src, filepath.Join(out, "wasm_exec.js"))
		}
	}
	return fmt.Errorf("wasm_exec.js not found in %s", goroot)
}

// buildInto builds the library in a temporary directory and copies only the
// library to dst.
func (b builder) buildInto(env []string, mode, dst string) error {
	tmp, err := os.MkdirTemp("", "fluffy-build")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	lib := filepath.Join(tmp, filepath.Base(dst))
	if err := b.goBuild(env, mode, lib); err != nil {
		return err
	}
	return copyFile(lib, dst)
}

func (b builder) goBuild(env []string, mode, output string) error {
	args := []string{"build", "-trimpath"}
	if b.strip {
		args = append(args, "-ldflags=-s -w")
	}
	if mode != "" {
		args = append(args, "-buildmode="+mode)
	}
	args = append(args, "-o", output, ".")

	fmt.Fprintf(os.Stderr, "fluffy: building golang/ (%s)\n", strings.Join(env, " "))
	cmd := exec.Command("go", args...)
	cmd.Dir = filepath.Join(b.root, "golang")
	// Later entries win, so these override anything inherited.
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}
	return nil
}

// installFat copies a single-architecture library to dst, or merges several
// into a universal binary with lipo.
func installFat(parts []string, dst string) error {
	if len(parts) == 1 {
		return copyFile(parts[0], dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("lipo", append(append([]string{"-create"}, parts...), "-output", dst)...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// findNDK locates the Android NDK from the environment, or in the Android
// SDK: the version the app pins (android.ndkVersion, passed by the Gradle
// hook), else the newest installed one.
func findNDK(root, version string) (string, error) {
	for _, key := range []string{"ANDROID_NDK_HOME", "ANDROID_NDK_ROOT", "ANDROID_NDK"} {
		if dir := os.Getenv(key); dir != "" {
			return dir, nil
		}
	}

	var sdks []string
	if props, err := os.ReadFile(filepath.Join(root, "android", "local.properties")); err == nil {
		for _, line := range strings.Split(string(props), "\n") {
			if value, ok := strings.CutPrefix(strings.TrimSpace(line), "sdk.dir="); ok {
				// local.properties escapes ':' and '\' on Windows.
				sdks = append(sdks, strings.NewReplacer(`\:`, ":", `\\`, `\`).Replace(value))
			}
		}
	}
	sdks = append(sdks, os.Getenv("ANDROID_HOME"), os.Getenv("ANDROID_SDK_ROOT"))
	if home, err := os.UserHomeDir(); err == nil {
		sdks = append(sdks,
			filepath.Join(home, "Android", "Sdk"),
			filepath.Join(home, "Library", "Android", "sdk"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Android", "Sdk"))
	}

	if version != "" {
		for _, sdk := range sdks {
			if sdk == "" {
				continue
			}
			dir := filepath.Join(sdk, "ndk", version)
			if _, err := os.Stat(filepath.Join(dir, "toolchains")); err == nil {
				return dir, nil
			}
		}
		fmt.Fprintf(os.Stderr, "fluffy: NDK %s is not installed, using the newest installed NDK\n", version)
	}
	for _, sdk := range sdks {
		if sdk == "" {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(sdk, "ndk"))
		if err != nil {
			continue
		}
		var newest string
		for _, entry := range entries {
			if entry.IsDir() && (newest == "" || compareVersions(entry.Name(), newest) > 0) {
				newest = entry.Name()
			}
		}
		if newest != "" {
			return filepath.Join(sdk, "ndk", newest), nil
		}
	}
	return "", errors.New("Android NDK not found, install it with the SDK manager or set ANDROID_NDK_HOME")
}

// compareVersions compares dotted numeric versions like 26.1.10909125.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x - y
		}
	}
	return 0
}

// output runs a command and returns its trimmed stdout.
func output(name string, args ...string) (string, error) {
	var stdout bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
