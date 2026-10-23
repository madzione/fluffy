# Testing a fluffy project

**Go:** functions and services are plain Go; test them with `go test ./...` in `golang/` (add `GOOS=js GOARCH=wasm go vet ./...` for the web build). The layer's own tests live in the fluffy repo, not in the project.

**Dart widget tests** run without the Go library through an in-memory fake Go side (`flutter test` still runs the build hook, so Go and `fluffy` must be on `PATH`). Handlers are keyed by **channel name**: `<Func>` for functions in `golang/` (e.g. `'Add'`), `<pkg>.<Func>` elsewhere, `<ns>.<Method>` for `Bind`, `<Iface>.<Method>` / `<pkg>.<Iface>.<Method>` for `//fluffy:dart` interfaces. Exported functions and bound methods receive their arguments as a **list**; `Handle`/`On` channels receive a single value. Return JSON-shaped values (maps for structs, ISO strings for times).

```dart
final go = golang.Bridge.fake();
go.receiver.handle('todo.Add', (data) {
  final [title as String] = data as List<dynamic>;
  return {'id': 1, 'title': title, 'done': false, 'created': '2026-01-01T00:00:00Z'};
});
go.receiver.stream('todo.Watch', (_) => Stream.value([]));
// Fake Go calling a Dart //fluffy:dart implementation:
final ok = await go.emitter.request<bool>('todo.UI.Confirm', ['Delete?']);
```

**Tests against the real library:** a plain `test()` in `test/` that calls the generated API without `Bridge.fake()` talks to the real Go library for the host. Integration tests: `flutter test integration_test -d linux` (the hook builds Go). For the web, `fluffy lib web` first, then `flutter drive ... -d web-server`.
