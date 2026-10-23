// Command fluffy integrates a Go library into a Flutter project and wraps the
// flutter tool so the Go code is built alongside the app.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

const version = "0.1.0"

const usage = `Fluffy - Go code in Flutter apps

Usage:
  fluffy create [--fvm <version>] [--patch-gtk] [flutter create flags] <dir>
                                               Create a Flutter project and run init in it.
                                               With fvm installed the project is pinned to
                                               --fvm, the .fvmrc of this or a parent dir, or
                                               the fvm global version
  fluffy init [--patch-gtk]                    Add golang/, Dart bindings and build hooks to the current project
  fluffy update [--patch-gtk]                  Update the communication layer (golang/fluffy, lib/golang/src)
                                               and re-apply the build hooks, keeps your Go and Dart code
  fluffy generate [--check | --watch]          Write the typed Dart API (lib/golang/api.g.dart) and the Go
                                               registrations (golang/fluffy_api.go) from the Go code.
                                               --check fails if they are out of date, --watch keeps
                                               them up to date while you edit
  fluffy run [flutter run flags]               Generate, build the Go library for web when needed, then flutter run
  fluffy build <target> [flutter build flags]  Generate, build the Go library for web, then flutter build <target>
  fluffy lib <target> [lib flags]              Build only the Go library for linux, windows, macos, android, ios or web
  fluffy --version                             Print fluffy and flutter versions
  fluffy <anything else>                       Passed through to flutter unchanged, without generating

Native targets build the Go library in a Flutter build hook (golang/hook/build.dart),
so plain "flutter run", "flutter build" and "flutter test" work too once the API is
generated: run fluffy generate yourself, or keep "fluffy generate --watch" running.
The hook fails when lib/golang/api.g.dart is out of date. Web has no hook: use
"fluffy run" and "fluffy build web", or "fluffy lib web" first.

--patch-gtk makes the Linux runner use the GTK header bar only on GNOME, so
other desktops (e.g. KDE on Wayland) draw their native title bar instead of
GTK's thick client-side one. Once applied, update keeps it up to date.

Lib flags:
  -o <dir>               Output directory
  --arch <list>          Architectures, e.g. "arm64 x86_64" (default: host, or all ABIs on android)
  --sdk <name>           iOS SDK: iphoneos or iphonesimulator (default: iphoneos)
  --android-api <level>  Android API level of the NDK compiler (default: 21)
  --ndk-version <ver>    Android NDK version in the SDK (default: the newest installed)
  --cc <path>            C compiler for cgo; on android the NDK's plain clang, which replaces
                         the NDK search
  --min-os <version>     Minimum macOS or iOS version (default on ios: $IPHONEOS_DEPLOYMENT_TARGET or 13.0)
  --deps <file>          Write the files the build depends on to file, one per line
  --strip                Leave out the symbol table and debug info, for smaller release builds
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "fluffy:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	case "--version":
		fmt.Printf("Fluffy %s • https://github.com/madzione/fluffy\n", version)
		return flutter(".", "--version")
	case "create":
		return create(args[1:])
	case "init", "update":
		patchGtk, rest := boolFlag(args[1:], "--patch-gtk")
		if len(rest) > 0 {
			return fmt.Errorf("unknown argument %q, usage: fluffy %s [--patch-gtk]", rest[0], args[0])
		}
		if args[0] == "init" {
			return initProject(".", false, patchGtk)
		}
		root, err := findProjectRoot()
		if err != nil {
			return err
		}
		return updateProject(root, patchGtk)
	case "generate":
		return generateCommand(args[1:])
	case "build":
		return buildCommand(args[1:])
	case "lib":
		return libCommand(args[1:])
	case "run":
		return runCommand(args[1:])
	default:
		return flutter(".", args...)
	}
}
