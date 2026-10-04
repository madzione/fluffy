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
func hooks(root string, patchGtk bool) []hook {
	gradle := filepath.Join("android", "app", "build.gradle.kts")
	gradleHook := gradleKtsHook
	if _, err := os.Stat(filepath.Join(root, gradle)); err != nil {
		gradle = filepath.Join("android", "app", "build.gradle")
		gradleHook = gradleGroovyHook
	}

	list := []hook{
		{"linux", filepath.Join("linux", "CMakeLists.txt"), appendBlock(hashMarker, cmakeHook("linux", "libgolang.so"))},
		{"windows", filepath.Join("windows", "CMakeLists.txt"), appendBlock(hashMarker, cmakeHook("windows", "golang.dll"))},
		{"android", gradle, appendBlock(slashMarker, gradleHook)},
		{"macos", filepath.Join("macos", "Runner.xcodeproj", "project.pbxproj"), xcodeHook(macosScript)},
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

// notFound is shown by every hook when the build cannot find fluffy, which
// otherwise fails with only "Build process failed" or a bare exit code.
const notFound = `fluffy was not found on PATH. The Go library of this app is built by the fluffy CLI during the Flutter build. ` +
	`Install it with "go install github.com/madzione/fluffy@latest" and make sure its directory ` +
	`(usually $(go env GOPATH)/bin, e.g. ~/go/bin) is on PATH, then build again.`

func cmakeHook(target, lib string) string {
	return fmt.Sprintf(`# Builds the Go library in golang/ on every build and bundles it with the app.
# Search again on every configure, a cached path breaks when fluffy moves.
unset(FLUFFY CACHE)
find_program(FLUFFY fluffy)
if(NOT FLUFFY)
  message(FATAL_ERROR %[3]q)
endif()
# The architecture flutter builds for (FLUTTER_TARGET_PLATFORM, e.g.
# linux-arm64), empty for the host's.
string(REGEX REPLACE "^[a-z]+-" "" FLUFFY_ARCH "${FLUTTER_TARGET_PLATFORM}")
add_custom_target(fluffy_golang ALL
  COMMAND "${FLUFFY}" lib %[1]s --arch "${FLUFFY_ARCH}" -o "${CMAKE_CURRENT_BINARY_DIR}/golang"
  WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}/.."
  COMMENT "Building Go library"
  VERBATIM
)
add_dependencies(${BINARY_NAME} fluffy_golang)
install(FILES "${CMAKE_CURRENT_BINARY_DIR}/golang/%[2]s"
  DESTINATION "${INSTALL_BUNDLE_LIB_DIR}" COMPONENT Runtime)`, target, lib, notFound)
}

var gradleKtsHook = `// Builds the Go library in golang/ into src/main/jniLibs before every build,
// for the ABIs flutter builds (only the device's on flutter run) and with the
// app's NDK.
val fluffyBuildGo = tasks.register<Exec>("fluffyBuildGo") {
    workingDir = rootDir.parentFile
    val abis = (project.findProperty("target-platform") ?: project.findProperty("android.injected.build.abi") ?: "").toString()
    commandLine("fluffy", "lib", "android", "--arch", abis.replace("android-", ""), "--ndk-version", android.ndkVersion ?: "")
    doFirst {
        val onPath = System.getenv("PATH").orEmpty().split(File.pathSeparator)
            .any { File(it, "fluffy").canExecute() || File(it, "fluffy.exe").canExecute() }
        if (!onPath) throw GradleException(` + kotlinString(notFound) + `)
    }
}
tasks.named("preBuild") { dependsOn(fluffyBuildGo) }`

var gradleGroovyHook = `// Builds the Go library in golang/ into src/main/jniLibs before every build,
// for the ABIs flutter builds (only the device's on flutter run) and with the
// app's NDK.
tasks.register("fluffyBuildGo", Exec) {
    workingDir = rootDir.parentFile
    def abis = (project.findProperty("target-platform") ?: project.findProperty("android.injected.build.abi") ?: "").toString()
    commandLine "fluffy", "lib", "android", "--arch", abis.replace("android-", ""), "--ndk-version", android.ndkVersion ?: ""
    doFirst {
        def onPath = (System.getenv("PATH") ?: "").split(File.pathSeparator)
            .any { new File(it, "fluffy").canExecute() || new File(it, "fluffy.exe").canExecute() }
        if (!onPath) throw new GradleException(` + kotlinString(notFound) + `)
    }
}
tasks.named("preBuild") { dependsOn "fluffyBuildGo" }`

// Builds started from the Xcode UI get a minimal PATH without Go or fluffy.
// Xcode shows lines starting with "error:" in its issue list.
var xcodePath = `export PATH="$PATH:$HOME/go/bin:/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin"
if ! command -v fluffy >/dev/null 2>&1; then
  echo ` + shellString("error: "+notFound) + `
  exit 1
fi`

var macosScript = `set -e
` + xcodePath + `
cd "$PROJECT_DIR/.."
fluffy lib macos --arch "$ARCHS" -o "$BUILT_PRODUCTS_DIR/$FRAMEWORKS_FOLDER_PATH"
codesign --force --sign "${EXPANDED_CODE_SIGN_IDENTITY:--}" "$BUILT_PRODUCTS_DIR/$FRAMEWORKS_FOLDER_PATH/libgolang.dylib"
`

var iosScript = `set -e
` + xcodePath + `
cd "$PROJECT_DIR/.."
fluffy lib ios --sdk "$PLATFORM_NAME" --arch "$ARCHS"
`

// iOS links the Go archive statically. Dart looks the symbols up at runtime,
// so they must survive stripping.
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
/android/app/src/main/jniLibs/*/libgolang.so
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

	if start := strings.Index(content, m.begin); start >= 0 {
		end := strings.Index(content[start:], m.end)
		if end < 0 {
			return "", fmt.Errorf("%q has no matching %q, remove the rest of the fluffy block or add the end marker", m.begin, m.end)
		}
		end += start + len(m.end)
		if end < len(content) && content[end] == '\n' {
			end++
		}
		return content[:lineStart(content, start)] + block.String() + content[end:], nil
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
		if start := strings.Index(content, "\n\t\t"+xcodePhaseID+" /* "+xcodePhaseName+" */ = {\n"); start >= 0 {
			start++
			end := strings.Index(content[start:], "\n\t\t};\n")
			if end < 0 {
				return "", errors.New("unterminated fluffy build phase")
			}
			content = content[:start] + content[start+end+len("\n\t\t};\n"):]
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

// kotlinString quotes s for Kotlin and Groovy. Groovy interpolates $ in
// double quoted strings, so it is escaped for both.
func kotlinString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`).Replace(s) + `"`
}

// shellString quotes s for sh.
func shellString(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}

// pbxString quotes s as an OpenStep plist string.
func pbxString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(s) + `"`
}
