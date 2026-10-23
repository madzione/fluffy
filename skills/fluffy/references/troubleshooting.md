# Troubleshooting a fluffy project

1. `fluffy --version` works and `which fluffy` is on the `PATH` the build sees (the hook and Xcode GUI builds add `~/go/bin`, `/usr/local/go/bin`, `/opt/homebrew/bin`, `/usr/local/bin`).
2. `fluffy generate` runs cleanly; read every warning. A native build failing with "fluffy generate failed … out of date" means the Go API changed: run `fluffy generate`, then build again.
3. `cd golang && go build ./... && GOOS=js GOARCH=wasm go vet ./...`.
4. `fluffy lib <target>` alone, to separate Go/cgo/NDK problems from Flutter ones. `flutter build <target> -v` shows the hook's full output. Android: set `ANDROID_NDK_HOME` or pass `--ndk-version`; Windows cross builds use `x86_64-w64-mingw32-gcc` unless `CC` is set. Remember that `CC`, `CGO_*`, `GOFLAGS`, … from the shell don't reach the build hook.
5. A Dart call fails with `GolangException`: the message is the Go error or panic, or the channel has no handler (missing `Bind`, `Start` not run, generated file stale → `fluffy generate`).
6. Web blank page or loader error: rebuild with `fluffy lib web` / `fluffy run -d chrome`, check `web/golang.wasm` and `web/wasm_exec.js` exist and the `fluffy:begin` block is in `web/index.html` (`fluffy update` restores it).
7. After upgrading fluffy, run `fluffy update` in each project. Build file patches from older versions keep working but print a hint.
8. Linux desktops other than GNOME show a thick GTK title bar: `fluffy update --patch-gtk` (or on `create`/`init`) fixes it.
