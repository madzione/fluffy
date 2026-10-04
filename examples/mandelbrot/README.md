# mandelbrot

![](./screenshot.png)

Heavy work in Go: the Mandelbrot set is rendered by one worker goroutine
per CPU and streamed to Flutter tile by tile, while the UI stays
responsive. Every change of the view cancels the running render, and the Go
workers stop. A second tab measures what the bridge itself costs. Created
with `fluffy create`.

```
golang/
  fractal/          the renderer, plain Go (go test ./fractal)
    yield_js.go     lets the browser run between tiles, see below
  bench/            Go methods that do nothing, for the benchmarks
  main.go           binds fractal.Renderer as "fractal" and bench.Bench as "bench"
lib/
  fractal.dart      FractalController: starts/cancels renders, assembles the image
  fractal_page.dart tap to zoom in, right click or long press to zoom out,
                    drag to move, mouse wheel to zoom
  bench.dart        the benchmarks, bench_page.dart shows them
test/               the controller against Bridge.fake() (receiver.stream plays Go)
integration_test/   real Go: full render, cancel stops the workers, zoom, benchmarks
```

```sh
fluffy run -d linux --release                     # or -d chrome
fvm flutter test
fvm flutter test integration_test -d linux        # benchmarks print at the end
(cd golang && go test -race ./...)
```

## How it works

`Renderer.Render(ctx, View) (<-chan Tile, error)` becomes
`Stream<Tile> golang.fractal.render(View)`. Workers take 16-row bands from
a queue, check `ctx` before every row and send finished tiles (RGBA bytes,
base64 on the wire) with a running `done`/`total` count. The controller
copies each tile into a frame buffer and decodes it into a `ui.Image`, at
most one decode at a time.

Starting a new render cancels the old subscription. fluffy cancels the Go
context, the workers stop after their current row, and `Stats` counts the
render as cancelled. The info card polls `golang.fractal.stats()`, so you
can watch "workers running" drop to 0 while you drag the iterations slider.

"Go workers busy … at once" is the time the workers spent on the tiles
divided by the wall time: how many CPUs the render really used.

### On the web

Go compiled to WebAssembly has one thread, the browser's main thread, and
only returns to the browser when every goroutine is blocked. A worker that
computes without blocking would freeze the page until the image is done,
because Flutter never gets to process a tile or draw a frame. `yield_js.go`
sleeps for a millisecond after each tile, which hands the thread back to the
browser. Natively `yield` does nothing. Any long computation in Go on the
web needs a pause like this.

## What the bridge costs

The Bridge tab, and the end of the integration test, run the benchmarks.
Measured on Linux (profile build, 12 CPUs) and in Chromium (release build,
JavaScript):

| Benchmark | Linux | Web |
|-----------|-------|-----|
| Go → Dart, one way | median 1.09 ms | — (same thread) |
| isolate → main isolate, Dart only | median 1.06 ms | — |
| one call at a time | 1.1 ms | 0.13 ms |
| 10 000 calls in flight | 30 µs each | 93 µs each |
| Dart → Go 1 MiB | 52 MB/s | 25 MB/s |
| Go → Dart 1 MiB | 91 MB/s | 110 MB/s |
| Go → Dart 16 MiB | 134 MB/s | 115 MB/s |
| stream values | 27 k/s | 55 k/s |
| stream 256 KiB chunks | 101 MB/s | 118 MB/s |

On Linux every message from Go waits about 1 ms before Dart handles it.
A message from another Dart isolate waits just as long, so the delay comes
from the Flutter Linux embedder scheduling work on its main thread, not
from fluffy. It limits calls made one after another, not throughput:
calls that are in flight together share the wait. Bytes travel as base64
inside JSON, so large payloads cost encoding time on both sides.
