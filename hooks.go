package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Every change fluffy makes to a generated platform file sits between
// "fluffy:begin" and "fluffy:end" markers, so init and update can replace it
// in place instead of adding it twice.
type marker struct{ begin, end string }

var (
	hashMarker  = marker{"# fluffy:begin", "# fluffy:end"}
	slashMarker = marker{"// fluffy:begin", "// fluffy:end"}
	htmlMarker  = marker{"<!-- fluffy:begin -->", "<!-- fluffy:end -->"}
)

type hook struct {
	platform string // directory that has to exist, empty for always
	file     string
	patch    func(content string) (string, error)
}

// hooks returns the patches for the project in root. The GTK patch is
// optional: it is added with patchGtk and kept once the runner has it.
//
// Linux, Windows, Android and macOS build the Go library in the Flutter
// build hook of golang/ (golang/hook/build.dart). The patches of their build
// files that older fluffy versions made are removed.
func hooks(root string, patchGtk bool) []hook {
	gradle := filepath.Join("android", "app", "build.gradle.kts")
	if _, err := os.Stat(filepath.Join(root, gradle)); err != nil {
		gradle = filepath.Join("android", "app", "build.gradle")
	}

	list := []hook{
		{"linux", filepath.Join("linux", "CMakeLists.txt"), removeBlock(hashMarker)},
		{"windows", filepath.Join("windows", "CMakeLists.txt"), removeBlock(hashMarker)},
		{"android", gradle, removeBlock(slashMarker)},
		{"macos", filepath.Join("macos", "Runner.xcodeproj", "project.pbxproj"), removeXcodePhase},
		{"ios", filepath.Join("ios", "Runner.xcodeproj", "project.pbxproj"), xcodeHook(iosScript)},
		{"ios", filepath.Join("ios", "Flutter", "Debug.xcconfig"), appendBlock(slashMarker, iosXcconfig)},
		{"ios", filepath.Join("ios", "Flutter", "Release.xcconfig"), appendBlock(slashMarker, iosXcconfig)},
		{"web", filepath.Join("web", "index.html"), patchIndexHTML},
		{"", ".gitignore", appendBlock(hashMarker, gitignore)},
	}

	runner := filepath.Join("linux", "runner", "my_application.cc")
	current, _ := os.ReadFile(filepath.Join(root, runner))
	if patchGtk || strings.Contains(string(current), slashMarker.begin) {
		list = append(list, hook{"linux", runner, patchGtkRunner})
	}
	return list
}

// applyHooks wires the Go build into every platform the project has.
// patchGtk also applies the optional GTK patch.
func applyHooks(root string, patchGtk bool) error {
	for _, h := range hooks(root, patchGtk) {
		if h.platform != "" {
			if _, err := os.Stat(filepath.Join(root, h.platform)); err != nil {
				continue
			}
		}

		path := filepath.Join(root, h.file)
		display := filepath.ToSlash(h.file)
		content, err := os.ReadFile(path)
		if err != nil && !(h.platform == "" && errors.Is(err, os.ErrNotExist)) {
			fmt.Printf("  skipped  %s (%v)\n", display, err)
			continue
		}

		patched, err := h.patch(string(content))
		if err != nil {
			return fmt.Errorf("%s: %w", display, err)
		}
		if patched == string(content) {
			fmt.Printf("  ok       %s\n", display)
			continue
		}
		if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
			return err
		}
		fmt.Printf("  patched  %s\n", display)
	}
	return nil
}

// removeOldLibraries deletes the Android libraries the Gradle hook of older
// fluffy versions built into the source tree. Gradle would package them
// next to the ones of the build hook.
func removeOldLibraries(root string) error {
	jniLibs := filepath.Join(root, "android", "app", "src", "main", "jniLibs")
	libs, _ := filepath.Glob(filepath.Join(jniLibs, "*", "libgolang.so"))
	for _, lib := range libs {
		if err := os.Remove(lib); err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, lib)
		fmt.Printf("  removed  %s\n", filepath.ToSlash(rel))
		// Only removes empty directories.
		os.Remove(filepath.Dir(lib))
	}
	if len(libs) > 0 {
		os.Remove(jniLibs)
	}
	return nil
}

// notFound is shown by the build hooks when the build cannot find fluffy,
// which otherwise fails with only "Build process failed" or a bare exit
// code. golang/hook/build.dart has a copy.
const notFound = `fluffy was not found on PATH. The Go library of this app is built by the fluffy CLI during the Flutter build. ` +
	`Install it with "go install github.com/madzione/fluffy@latest" and make sure its directory ` +
	`(usually $(go env GOPATH)/bin, e.g. ~/go/bin) is on PATH, then build again.`

// Builds started from the Xcode UI get a minimal PATH without Go or fluffy.
// Xcode shows lines starting with "error:" in its issue list.
var xcodePath = `export PATH="$PATH:$HOME/go/bin:/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin"
if ! command -v fluffy >/dev/null 2>&1; then
  echo ` + shellString("error: "+notFound) + `
  exit 1
fi`

var iosScript = `set -e
` + xcodePath + `
cd "$PROJECT_DIR/.."
fluffy lib ios --sdk "$PLATFORM_NAME" --arch "$ARCHS"
`

// iOS links the Go archive statically: Go can't build a shared library for
// iOS, and Flutter only bundles shared ones. The build hook declares the
// library as found in the process. Dart looks the symbols up at runtime, so
// they must survive stripping.
const iosXcconfig = `OTHER_LDFLAGS = $(inherited) -force_load "$(PROJECT_DIR)/../build/fluffy/ios/$(PLATFORM_NAME)/libgolang.a" -framework CoreFoundation -framework Security -lresolv
STRIP_STYLE = non-global`

// Go has to serve before Dart connects to it, so the block starts Go, waits
// until fluffy.Serve calls fluffyReady and only then loads Flutter. When Go
// fails to load or exits first, the page shows why instead of staying blank.
const webLoader = `<script src="wasm_exec.js"></script>
<script>
  const go = new Go();
  const goReady = new Promise((resolve) => (globalThis.fluffyReady = resolve));
  WebAssembly.instantiateStreaming(fetch("golang.wasm"), go.importObject)
    .then((result) => Promise.race([
      goReady,
      go.run(result.instance).then(() => {
        throw new Error("the Go program exited before it called fluffy.Serve()");
      }),
    ]))
    .then(() => {
      const flutter = document.createElement("script");
      flutter.src = "flutter_bootstrap.js";
      flutter.async = true;
      document.body.appendChild(flutter);
    })
    .catch((error) => {
      console.error(error);
      const message = document.createElement("pre");
      message.style.cssText = "margin: 16px; color: #b00020; white-space: pre-wrap;";
      message.textContent = "The Go library (golang.wasm) failed to start: " + error +
        "\nBuild it with \"fluffy lib web\" (fluffy run and fluffy build web do it for you).";
      document.body.appendChild(message);
    });
</script>`

const gitignore = `# Go libraries built by fluffy
/web/golang.wasm
/web/wasm_exec.js`

// Flutter's Linux runner uses a GTK header bar unless it runs on X11 under a
// window manager other than GNOME Shell. On Wayland outside GNOME, e.g. KDE,
// that is a thick client-side title bar next to the app's own UI. A plain
// title instead lets the compositor draw its native decoration.
const gtkHeaderBar = `// Header bar only on GNOME, elsewhere the compositor decorates the window.
const gchar* desktop = g_getenv("XDG_CURRENT_DESKTOP");
if (desktop == nullptr || g_strstr_len(desktop, -1, "GNOME") == nullptr) {
  use_header_bar = FALSE;
}`

const gtkAnchor = "if (use_header_bar) {"

func patchGtkRunner(content string) (string, error) {
	if !strings.Contains(content, slashMarker.begin) && !strings.Contains(content, gtkAnchor) {
		return "", fmt.Errorf("--patch-gtk: no %q found, the runner differs from Flutter's template", gtkAnchor)
	}
	return upsertBlock(content, slashMarker, gtkHeaderBar, "  ", gtkAnchor)
}

var flutterBootstrapTag = regexp.MustCompile(`(?m)^[ \t]*<script[^>]*src="flutter_bootstrap\.js"[^>]*>\s*</script>[ \t]*\n?`)

func patchIndexHTML(content string) (string, error) {
	content = flutterBootstrapTag.ReplaceAllString(content, "")
	return upsertBlock(content, htmlMarker, webLoader, "  ", "</body>")
}

// removeBlock removes the marked block and the empty line appendBlock put
// before it.
func removeBlock(m marker) func(string) (string, error) {
	return func(content string) (string, error) {
		start, end, err := findBlock(content, m)
		if err != nil || start < 0 {
			return content, err
		}
		if strings.HasSuffix(content[:start], "\n\n") {
			start--
		}
		return content[:start] + content[end:], nil
	}
}

func appendBlock(m marker, body string) func(string) (string, error) {
	return func(content string) (string, error) {
		return upsertBlock(content, m, body, "", "")
	}
}

// upsertBlock replaces the marked block in content. Without one it inserts
// the block on the line before anchor, or appends it when there is no anchor.
// A begin marker without an end marker is an error, guessing where the
// block ended could remove the user's code.
func upsertBlock(content string, m marker, body, indent, anchor string) (string, error) {
	lines := []string{m.begin}
	lines = append(lines, strings.Split(strings.TrimRight(body, "\n"), "\n")...)
	lines = append(lines, m.end)
	var block strings.Builder
	for _, line := range lines {
		if line != "" {
			block.WriteString(indent)
		}
		block.WriteString(line + "\n")
	}

	start, end, err := findBlock(content, m)
	if err != nil {
		return "", err
	}
	if start >= 0 {
		return content[:start] + block.String() + content[end:], nil
	}

	if anchor != "" {
		if at := strings.LastIndex(content, anchor); at >= 0 {
			at = lineStart(content, at)
			return content[:at] + block.String() + content[at:], nil
		}
	}
	if content != "" {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n"
	}
	return content + block.String(), nil
}

// findBlock returns the lines of the marked block in content, from the
// start of the begin marker's line to after the end marker's line, or -1
// when there is none. A begin marker without an end marker is an error,
// guessing where the block ended could remove the user's code.
func findBlock(content string, m marker) (start, end int, err error) {
	start = strings.Index(content, m.begin)
	if start < 0 {
		return -1, -1, nil
	}
	end = strings.Index(content[start:], m.end)
	if end < 0 {
		return -1, -1, fmt.Errorf("%q has no matching %q, remove the rest of the fluffy block or add the end marker", m.begin, m.end)
	}
	end += start + len(m.end)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return lineStart(content, start), end, nil
}

func lineStart(content string, i int) int {
	return strings.LastIndex(content[:i], "\n") + 1
}

// The build phase uses a fixed object ID so it can be found again on update.
const xcodePhaseID = "F1FF1E000000000000000001"
const xcodePhaseName = "Build Go library (fluffy)"

var runnerTarget = regexp.MustCompile(`(?m)^\t\t[0-9A-F]{24} /\* Runner \*/ = \{\n\t\t\tisa = PBXNativeTarget;`)

// xcodeHook adds a shell script build phase to the Runner target. It runs
// first, so the library exists before Xcode links and bundles the app.
func xcodeHook(script string) func(string) (string, error) {
	return func(content string) (string, error) {
		const section = "/* Begin PBXShellScriptBuildPhase section */\n"
		phase := "\t\t" + xcodePhaseID + " /* " + xcodePhaseName + " */ = {\n" +
			"\t\t\tisa = PBXShellScriptBuildPhase;\n" +
			"\t\t\talwaysOutOfDate = 1;\n" +
			"\t\t\tbuildActionMask = 2147483647;\n" +
			"\t\t\tfiles = (\n\t\t\t);\n" +
			"\t\t\tinputPaths = (\n\t\t\t);\n" +
			"\t\t\tname = " + pbxString(xcodePhaseName) + ";\n" +
			"\t\t\toutputPaths = (\n\t\t\t);\n" +
			"\t\t\trunOnlyForDeploymentPostprocessing = 0;\n" +
			"\t\t\tshellPath = /bin/sh;\n" +
			"\t\t\tshellScript = " + pbxString(script) + ";\n" +
			"\t\t};\n"

		// Replace an earlier version of the phase.
		content, err := cutXcodePhase(content)
		if err != nil {
			return "", err
		}
		at := strings.Index(content, section)
		if at < 0 {
			return "", errors.New("no PBXShellScriptBuildPhase section, is this a Flutter Runner project?")
		}
		at += len(section)
		content = content[:at] + phase + content[at:]

		loc := runnerTarget.FindStringIndex(content)
		if loc == nil {
			return "", errors.New("Runner target not found")
		}
		targetEnd := strings.Index(content[loc[0]:], "\n\t\t};\n")
		if targetEnd < 0 {
			return "", errors.New("unterminated Runner target")
		}
		targetEnd += loc[0]
		if strings.Contains(content[loc[0]:targetEnd], xcodePhaseID) {
			return content, nil
		}
		const phases = "buildPhases = (\n"
		p := strings.Index(content[loc[0]:targetEnd], phases)
		if p < 0 {
			return "", errors.New("Runner target has no buildPhases")
		}
		p += loc[0] + len(phases)
		return content[:p] + "\t\t\t\t" + xcodePhaseID + " /* " + xcodePhaseName + " */,\n" + content[p:], nil
	}
}

// cutXcodePhase removes the definition of the build phase.
func cutXcodePhase(content string) (string, error) {
	start := strings.Index(content, "\n\t\t"+xcodePhaseID+" /* "+xcodePhaseName+" */ = {\n")
	if start < 0 {
		return content, nil
	}
	start++
	end := strings.Index(content[start:], "\n\t\t};\n")
	if end < 0 {
		return "", errors.New("unterminated fluffy build phase")
	}
	return content[:start] + content[start+end+len("\n\t\t};\n"):], nil
}

// removeXcodePhase removes the build phase and its use in the Runner target.
func removeXcodePhase(content string) (string, error) {
	content, err := cutXcodePhase(content)
	if err != nil {
		return "", err
	}
	return strings.Replace(content, "\t\t\t\t"+xcodePhaseID+" /* "+xcodePhaseName+" */,\n", "", 1), nil
}

// shellString quotes s for sh.
func shellString(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}

// pbxString quotes s as an OpenStep plist string.
func pbxString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(s) + `"`
}
