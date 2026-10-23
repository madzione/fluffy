---
name: fluffy
description: How to work with fluffy, the CLI that compiles a project's Go code into its Flutter app (dart:ffi natively, WebAssembly on the web) and generates a typed Dart API from it. Use when writing or changing Go code under `golang/`, calling Go from Dart or Dart from Go, touching `lib/golang/`, `api.g.dart`, `fluffy_api.go`, `//fluffy:export` or `//fluffy:dart`, adding fluffy to a Flutter project, or building, running, testing or debugging a fluffy project. Not needed for Flutter/Dart work that doesn't involve the Go side.
---

# fluffy: Go in Flutter apps

fluffy compiles the Go code in `golang/` into a C library called over `dart:ffi` (Linux, Windows, macOS, Android, iOS) or into WebAssembly (web), and generates a typed Dart API from the Go signatures. There is no IDL and no hand-written glue. Go can also call Dart.

Most mistakes come from editing files fluffy owns, from code the generator can't see, or from setup in `main` (native platforms never run it).

## Setup facts

- `fluffy` (install: `go install github.com/madzione/fluffy@latest`) must be on `PATH` (usually `~/go/bin`): the build hook in `golang/hook/build.dart` calls it during `flutter run/build/test`. "fluffy not found" during a build means `PATH`, not code. Needs Flutter 3.38+; native targets need cgo (C compiler, MinGW-w64 on Windows, Xcode, Android NDK).
- Build hooks only see some env vars: set Go settings (`GOPRIVATE`, `GOPROXY`, `GOFLAGS`) with `go env -w`, not in the shell.
- With a `.fvmrc`, use `fvm flutter ...` (fluffy does this itself). `fluffy help` prints the full usage.

## Who owns what

```
golang/                 Go module "<package>/golang"
  go.mod, api.go        yours (api.go: //fluffy:export functions)
  main.go               yours: fluffy.Start setup in init(), main() only calls fluffy.Serve()
  fluffy_api.go         GENERATED
  pubspec.yaml, hook/, fluffy/   OWNED BY FLUFFY
lib/golang/
  golang.dart           yours: import 'package:<app>/golang/golang.dart' as golang;
  api.dart              yours: hand-written additions, must `export 'api.g.dart';`
  api.g.dart            GENERATED
  src/                  OWNED BY FLUFFY
```

Never edit generated or fluffy-owned files or add files there: `fluffy generate`/`update` overwrite them and delete unknown files. Change the Go code and regenerate. Commit the generated files.

Go code can live in `golang/`, its subpackages, or a module required with a local `replace` / `go.work`. Module-cache packages are only used for their types: bind their services from `golang/`.

## Commands

| Command | What it does |
|---|---|
| `fluffy generate [--check\|--watch]` | Writes `api.g.dart` and `fluffy_api.go`. `--check` fails when stale (CI). |
| `fluffy run [flutter run flags]` | Generate, build web wasm if needed, `flutter run`. |
| `fluffy build <target> [flags]` | Generate, build wasm for `web`, `flutter build`. |
| `fluffy lib <linux\|windows\|macos\|android\|ios\|web>` | Builds **only** the Go library (`-o`, `--arch`, `--ndk-version`, `--strip`, …). |
| `fluffy create [--fvm <ver>] [--patch-gtk] [flags] <dir>` | `flutter create` + `init` with a Go demo; dir must be the **last** argument. |
| `fluffy init` / `fluffy update` | Add fluffy to an existing app / refresh the layer and patches after upgrading fluffy. Keep user code. |
| `fluffy <other>` | Passed to `flutter` without generating (`fluffy test`, `fluffy analyze`). |

Plain `flutter run` builds Go on native targets but does **not** regenerate: the hook fails with "out of date: … run fluffy generate". Prefer `fluffy run`, or run `fluffy generate` after every Go change. The web has no hook: use `fluffy run -d chrome` / `fluffy build web`, or `fluffy lib web` before a plain `flutter` command.

## The loop

1. Change Go code (must build for native **and** `js/wasm`).
2. `fluffy generate` and read every warning: each one is code that silently didn't make it into the API.
3. Use the generated Dart; read `lib/golang/api.g.dart` for exact names and types.
4. Check: in `golang/` `go vet ./... && GOOS=js GOARCH=wasm go vet ./... && go test ./...`; in the app `flutter analyze`, `flutter test`, `fluffy run -d <device>`.

## Exposing Go

| Need | Use |
|---|---|
| Stateless function | `//fluffy:export` in its doc comment → `golang.add(...)`, `golang.<pkg>.<func>(...)` outside `golang/` |
| Stateful service set up once | `fluffy.Start(func() error { …; fluffy.Bind("todo", svc); return nil })` in `init()` → `golang.todo.add(...)` |
| Result over time | return `<-chan T` → Dart `Stream<T>` (cancel = ctx cancelled; always close the channel) |
| Go calling Dart | interface marked `//fluffy:dart` → generated `new<Name>()` in Go, abstract class + `register` in Dart |
| Untyped events | `fluffy.Emit` / `On` / `Handle` / `Request` with **constant** channel names |

Signatures: optional `context.Context` first, no variadics; results nothing, `R`, `error` or `(R, error)`. Errors and panics fail the Dart future with `golang.GolangException`. **Native platforms never run `main`**: all setup goes in `fluffy.Start`.

Details, examples and rules for each: [references/api.md](references/api.md). Go ↔ Dart type mapping: [references/types.md](references/types.md).

## Key gotchas

- **The generator type-checks as `GOOS=js GOARCH=wasm`.** Files excluded from js (`//go:build !js`, `_windows.go`, cgo) are invisible to it. Keep exports, bound services and `//fluffy:dart` interfaces in files that build everywhere; split only internals (`store.go` + `store_js.go`).
- **Long loops freeze the web app** (Go wasm runs on the browser's main thread): call `fluffy.Yield()` periodically and stream partial results; don't use `time.Sleep` for this.
- **Generator errors** (channel registered twice, conflicting types, Dart name clash, unsupported type) mean the runtime would misbehave: fix the Go code.
- **Hot restart** cancels Go streams and pending requests to Dart but keeps Go state; `Start` doesn't run again.

Testing (widget tests with `Bridge.fake()`, integration tests): [references/testing.md](references/testing.md). Build or runtime failures: [references/troubleshooting.md](references/troubleshooting.md).
