package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertBlockIsIdempotent(t *testing.T) {
	original := "cmake_minimum_required(VERSION 3.14)\ninstall(FILES a)"

	once, _ := upsertBlock(original, hashMarker, "first", "", "")
	want := "cmake_minimum_required(VERSION 3.14)\ninstall(FILES a)\n\n# fluffy:begin\nfirst\n# fluffy:end\n"
	if once != want {
		t.Fatalf("append:\n%q\nwant\n%q", once, want)
	}
	if twice, _ := upsertBlock(once, hashMarker, "first", "", ""); twice != once {
		t.Fatalf("second upsert changed content:\n%s", twice)
	}

	replaced, _ := upsertBlock(once, hashMarker, "second", "", "")
	if strings.Contains(replaced, "first") || strings.Count(replaced, "fluffy:begin") != 1 {
		t.Fatalf("replace:\n%s", replaced)
	}

	// A hand-edited file without the end marker is not patched a second time.
	broken := strings.Replace(once, "# fluffy:end\n", "", 1)
	if got, err := upsertBlock(broken, hashMarker, "second", "", ""); err == nil {
		t.Errorf("patched a block without end marker:\n%s", got)
	}
}

func TestPatchIndexHTML(t *testing.T) {
	original := readFixture(t, "index.html")

	patched, err := patchIndexHTML(original)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(patched, `<script src="flutter_bootstrap.js"`) {
		t.Error("flutter_bootstrap.js is still loaded directly")
	}
	block := strings.Index(patched, "<!-- fluffy:begin -->")
	if block < 0 || block > strings.Index(patched, "</body>") {
		t.Errorf("loader block missing or outside <body>:\n%s", patched)
	}

	again, _ := patchIndexHTML(patched)
	if again != patched {
		t.Errorf("second patch changed content:\n%s", again)
	}
}

func TestPatchGtkRunner(t *testing.T) {
	original := readFixture(t, "my_application.cc")

	patched, err := patchGtkRunner(original)
	if err != nil {
		t.Fatal(err)
	}
	block := strings.Index(patched, "  // fluffy:begin\n")
	if block < 0 || block > strings.Index(patched, gtkAnchor) || block < strings.Index(patched, "gboolean use_header_bar") {
		t.Errorf("block missing or not right before %q:\n%s", gtkAnchor, patched)
	}
	if again, _ := patchGtkRunner(patched); again != patched {
		t.Errorf("second patch changed content:\n%s", again)
	}
	if _, err := patchGtkRunner("int main() {}\n"); err == nil {
		t.Error("patched a runner without the header bar switch")
	}
}

// The GTK patch is opt-in and kept by later runs without the flag.
func TestGtkHookOptIn(t *testing.T) {
	root := t.TempDir()
	runner := filepath.Join(root, "linux", "runner", "my_application.cc")
	os.MkdirAll(filepath.Dir(runner), 0o755)
	os.WriteFile(runner, []byte(readFixture(t, "my_application.cc")), 0o644)

	has := func(patchGtk bool) bool {
		for _, h := range hooks(root, patchGtk) {
			if strings.HasSuffix(h.file, "my_application.cc") {
				return true
			}
		}
		return false
	}
	if has(false) || !has(true) {
		t.Fatal("GTK hook must only be added with --patch-gtk")
	}
	if err := applyHooks(root, true); err != nil {
		t.Fatal(err)
	}
	if !has(false) {
		t.Error("GTK hook is not kept once applied")
	}
}

func TestXcodeHook(t *testing.T) {
	for _, fixture := range []string{"ios.pbxproj", "macos.pbxproj"} {
		t.Run(fixture, func(t *testing.T) {
			original := readFixture(t, fixture)

			patched, err := xcodeHook("echo one\n")(original)
			if err != nil {
				t.Fatal(err)
			}
			// Object definition plus one reference in the Runner target.
			if n := strings.Count(patched, xcodePhaseID); n != 2 {
				t.Fatalf("phase ID appears %d times, want 2", n)
			}
			target := runnerTarget.FindStringIndex(patched)
			if !strings.Contains(patched[target[0]:target[0]+400], "buildPhases = (\n\t\t\t\t"+xcodePhaseID) {
				t.Error("phase is not the first build phase of Runner")
			}

			again, _ := xcodeHook("echo one\n")(patched)
			if again != patched {
				t.Error("second patch changed content")
			}

			updated, _ := xcodeHook("echo two\n")(patched)
			if strings.Contains(updated, "echo one") || !strings.Contains(updated, `shellScript = "echo two\n";`) {
				t.Error("script was not replaced")
			}
			if n := strings.Count(updated, xcodePhaseID); n != 2 {
				t.Fatalf("after update phase ID appears %d times, want 2", n)
			}

			// A Runner target cut off by hand is an error, not a panic.
			loc := runnerTarget.FindStringIndex(original)
			cut := "/* Begin PBXShellScriptBuildPhase section */\n" + original[loc[0]:loc[1]] + "\n\t\t\tbuildPhases = (\n"
			if _, err := xcodeHook("echo one\n")(cut); err == nil || !strings.Contains(err.Error(), "unterminated Runner target") {
				t.Errorf("unterminated Runner target: %v", err)
			}

			if plutil, err := exec.LookPath("plutil"); err == nil {
				file := filepath.Join(t.TempDir(), "project.pbxproj")
				os.WriteFile(file, []byte(updated), 0o644)
				if out, err := exec.Command(plutil, "-lint", file).CombinedOutput(); err != nil {
					t.Errorf("plutil: %s", out)
				}
			}
		})
	}
}

func TestDeviceArg(t *testing.T) {
	cases := map[string]string{
		"-d chrome":          "chrome",
		"-dchrome":           "chrome",
		"--device-id linux":  "linux",
		"--device-id=edge":   "edge",
		"--release -d macos": "macos",
	}
	for args, want := range cases {
		if got, ok := deviceArg(strings.Fields(args)); !ok || got != want {
			t.Errorf("deviceArg(%q) = %q, %v, want %q", args, got, ok, want)
		}
	}
	if _, ok := deviceArg([]string{"--release"}); ok {
		t.Error("found a device without -d")
	}
}

func TestFvmFlag(t *testing.T) {
	cases := map[string][2]string{
		"myapp":                              {"", "myapp"},
		"--fvm 3.24.0 myapp":                 {"3.24.0", "myapp"},
		"--org com.example --fvm=stable app": {"stable", "--org com.example app"},
	}
	for args, want := range cases {
		version, rest, err := fvmFlag(strings.Fields(args))
		if err != nil || version != want[0] || strings.Join(rest, " ") != want[1] {
			t.Errorf("fvmFlag(%q) = %q, %q, %v, want %q, %q", args, version, rest, err, want[0], want[1])
		}
	}
	for _, args := range []string{"--fvm", "--fvm --org com.example app"} {
		if _, _, err := fvmFlag(strings.Fields(args)); err == nil {
			t.Errorf("fvmFlag(%q) accepted --fvm without a version", args)
		}
	}
}

func TestParseArchs(t *testing.T) {
	got, err := parseArchs("arm64 x86_64,armeabi-v7a")
	if err != nil || strings.Join(got, " ") != "arm64 amd64 arm" {
		t.Fatalf("parseArchs = %v, %v", got, err)
	}
	if _, err := parseArchs("sparc"); err == nil {
		t.Error("unknown architecture accepted")
	}
}

func TestHasDependency(t *testing.T) {
	pubspec := "name: demo\n\ndependencies:\n  flutter:\n    sdk: flutter\n\n  # comment\n  http: ^1.0.0\n\ndev_dependencies:\n  ffi: ^2.0.0\n"
	if hasDependency([]byte(pubspec), "ffi") {
		t.Error("ffi under dev_dependencies counts as a dependency")
	}
	if !hasDependency([]byte(pubspec), "http") || !hasDependency([]byte(strings.ReplaceAll(pubspec, "\n", "\r\n")), "http") {
		t.Error("http not found under dependencies")
	}
}

// Hooks of older fluffy versions call "fluffy build <target>" for the Go
// library only, it must not start a flutter build from inside one.
func TestFromOldHook(t *testing.T) {
	t.Setenv(insideFlutter, "")
	for args, want := range map[string]bool{
		"linux -o build/golang":               true,
		"macos --arch arm64 -o out":           true,
		"ios --sdk iphonesimulator":           true,
		"android":                             true,
		"linux":                               false,
		"linux --release":                     false,
		"web -o out":                          false, // flutter build web -o
		"apk --target-platform android-arm64": false,
	} {
		if got := fromOldHook(strings.Fields(args)); got != want {
			t.Errorf("fromOldHook(%q) = %v, want %v", args, got, want)
		}
	}
	t.Setenv(insideFlutter, "1")
	if !fromOldHook([]string{"linux"}) {
		t.Error("fluffy build linux inside flutter is not from a hook")
	}
}

func TestCompareVersions(t *testing.T) {
	if compareVersions("28.2.13676358", "26.1.10909125") <= 0 || compareVersions("9.0", "10.0") >= 0 {
		t.Error("wrong version order")
	}
}

// The Go template and its generated registrations have to compile natively
// (cgo) and for the web, and the message layer has to pass its tests. They
// run in the template directory, user projects don't get them.
func TestGoTemplate(t *testing.T) {
	root := t.TempDir()
	if err := scaffold(root, "_templates/golang", "golang", templateData{"demo"}, false); err != nil {
		t.Fatal(err)
	}
	if err := syncLayer(root, templateData{"demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := generate(root, false); err != nil {
		t.Fatal(err)
	}
	for _, env := range [][]string{{"CGO_ENABLED=1"}, {"GOOS=js", "GOARCH=wasm"}} {
		cmd := exec.Command("go", "vet", "./...")
		cmd.Dir = filepath.Join(root, "golang")
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("go vet with %v: %v\n%s", env, err, out)
		}
	}
	cmd := exec.Command("go", "test", "-race", ".")
	cmd.Dir = filepath.Join("_templates", "layer", "golang")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("go test: %v\n%s", err, out)
	}
}

// The layer is replaced on every sync, the user's files next to it are kept.
func TestSyncLayer(t *testing.T) {
	root := t.TempDir()
	data := templateData{"demo"}
	if err := scaffold(root, "_templates/golang", "golang", data, false); err != nil {
		t.Fatal(err)
	}
	if err := syncLayer(root, data); err != nil {
		t.Fatal(err)
	}

	layerTest := filepath.Join(root, "golang", "fluffy", "fluffy_test.go")
	if _, err := os.Stat(layerTest); !os.IsNotExist(err) {
		t.Error("the layer's tests were copied into the project")
	}

	changed := filepath.Join(root, "golang", "fluffy", "bridge.go")
	stale := filepath.Join(root, "lib", "golang", "src", "old.dart")
	user := filepath.Join(root, "golang", "api.go")
	original, _ := os.ReadFile(changed)
	os.WriteFile(changed, []byte("package fluffy\n"), 0o644)
	os.WriteFile(stale, []byte("// removed from the templates\n"), 0o644)
	os.WriteFile(user, []byte("package main\n"), 0o644)

	if err := syncLayer(root, data); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(changed); string(got) != string(original) {
		t.Error("changed layer file was not replaced")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("file missing from the templates was not removed")
	}
	if got, _ := os.ReadFile(user); string(got) != "package main\n" {
		t.Error("user file was changed")
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
