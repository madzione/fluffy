# Exposing Go to Dart (and Dart to Go)

## `//fluffy:export` functions

```go
// Add adds a and b.
//
//fluffy:export
func Add(a, b int64) int64 { return a + b }

//fluffy:export
func FindUser(ctx context.Context, id string) (*User, error) { ... }
```

```dart
final sum = await golang.add(2, 3);      // Future<int>
final user = await golang.findUser('42'); // Future<User?>, User is a generated class
```

- The directive must be in the doc comment directly attached to the func. A blank line between comment and `func` detaches it (the generator warns).
- Parameters: optional `context.Context` first, then any arguments. No variadic functions.
- Results: nothing, `R`, `error` or `(R, error)`. A Go error (or panic) fails the Dart future with `golang.GolangException` (`e.message`).
- Functions in `golang/` itself may be unexported and become top-level Dart functions (`Add` → `golang.add`). Functions in other packages must be exported and are reached through the package name: `todo.Version` → `golang.todo.version()`.
- Go doc comments become Dart doc comments.

## Services: `fluffy.Start` and `fluffy.Bind`

The service package stays plain Go (no fluffy import) and testable with `go test`. Wire it up in `golang/main.go`:

```go
func init() {
	fluffy.Start(func() error {
		db, err := openDB()
		if err != nil {
			return err
		}
		fluffy.Bind("todo", todo.NewService(db))
		return nil
	})
}
```

```dart
await golang.todo.add('Milk');
final items = await golang.todo.list(); // List<Item>
```

- **Native platforms load Go as a library and never run `main`.** Put setup in `fluffy.Start` (called from `init`), never in `main`; `main` only calls `fluffy.Serve()` for the web. `Start` runs once on Dart's first connect (not again on hot restart); requests wait for it, and if it returns an error every request fails with that error.
- `Bind` registers every exported method with a supported signature (same rules as functions) as channel `<ns>.<Method>`; others are skipped with a generator warning. `fluffy.Bind[todo.API]("todo", svc)` exposes only the methods of interface `todo.API`.
- In Dart each namespace is a class `Go<Ns>` reached through the constant `golang.<ns>`.

## Streams: `<-chan T` results

```go
//fluffy:export
func Clock(ctx context.Context) <-chan string {
	out := make(chan string)
	go func() {
		defer close(out)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case now := <-t.C:
				select {
				case out <- now.Format("15:04:05"):
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}
```

Go runs the function when Dart starts listening; cancelling the Dart subscription cancels `ctx`. Always close the channel, also on `ctx.Done()`. Streams have backpressure: Go stops receiving from the channel while 1024 values or 8 MiB haven't reached the Dart listener (e.g. a paused subscription), so a fast producer simply blocks on its send. The `ctx` of non-stream functions is never cancelled.

## Go calling Dart: `//fluffy:dart`

```go
//fluffy:dart
type UI interface {
	Confirm(ctx context.Context, question string) (bool, error) // request: waits for Dart
	Progress(percent float64)                                    // emit: fire and forget
}
```

The generator writes a constructor into `fluffy_api.go`: `newUI()` for an interface in `golang/`, `new<Pkg><Name>()` (e.g. `newTodoUI()`) for one in another package. Inject its result like any dependency: `fluffy.Bind("counter", &Counter{ui: newUI()})`. The interface can live in the service package, which then still doesn't import fluffy. Give request methods a `ctx`; without one they wait forever.

In Dart, implement the generated abstract class (named `UI`, or `TodoUI` for package `todo`) and register it early, e.g. in `main()`:

```dart
class AppUI implements golang.UI {
  @override
  Future<bool> confirm(String question) => showConfirmDialog(question);
  @override
  void progress(double percent) {}
}

golang.UI.register(AppUI());
```

## Raw channels

`fluffy.Handle[T, R](name, fn)`, `fluffy.On[T](name, fn)`, `fluffy.Emit(name, data)` and `fluffy.Request[R](ctx, name, data)` in Go; `golang.emitter.emit/request/subscribe` and `golang.receiver.on/handle/remove` in Dart. The generator types them only when the channel name is a **constant**; a dotted name like `"todo.progress"` goes into the `golang.todo` namespace. `Emit`s before Dart connects are queued (up to 1024); emits to a channel nobody listens to are dropped. A Dart handler that throws gives Go a `*fluffy.DartError`. Register handlers in `init` or `fluffy.Start`.
