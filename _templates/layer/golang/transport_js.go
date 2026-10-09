//go:build js && wasm

package fluffy

import (
	"sync"
	"syscall/js"
)

// Serve registers the web transport on globalThis.golang and keeps the Go
// runtime alive. Dart passes two functions to golang.connect: Go calls the
// first with a message as a Uint8Array, and the second, the doorbell, when
// more messages wait for golang.take. Dart sends each message with
// golang.send. Then Serve calls globalThis.fluffyReady, which the loader in
// web/index.html waits for before it starts Flutter.
func Serve() {
	js.Global().Set("golang", js.ValueOf(map[string]any{
		"connect": js.FuncOf(func(this js.Value, args []js.Value) any {
			receive, ring := args[0], args[1]
			connectPush(func() { ring.Invoke() }, func(segments [][]byte, size int) {
				receive.Invoke(toJS(segments, size))
			})
			return nil
		}),
		"send": js.FuncOf(func(this js.Value, args []js.Value) any {
			raw := make([]byte, args[0].Length())
			js.CopyBytesToGo(raw, args[0])
			resumePush()
			deliver(raw, nil)
			return nil
		}),
		"take": js.FuncOf(func(this js.Value, args []js.Value) any {
			segments, size := take()
			if size == 0 {
				return nil
			}
			return toJS(segments, size)
		}),
	}))
	if ready := js.Global().Get("fluffyReady"); ready.Type() == js.TypeFunction {
		ready.Invoke()
	}
	select {}
}

var uint8Array = js.Global().Get("Uint8Array")

// toJS copies frames into a new Uint8Array.
func toJS(segments [][]byte, size int) js.Value {
	buf := segments[0]
	if len(segments) > 1 {
		buf = make([]byte, 0, size)
		for _, s := range segments {
			buf = append(buf, s...)
		}
	}
	frames := uint8Array.New(size)
	js.CopyBytesToJS(frames, buf)
	return frames
}

// yielding holds the goroutines waiting in Yield, in the order of their
// messages.
var yielding struct {
	sync.Mutex
	port    js.Value
	waiters []chan struct{}
}

// Yield hands the browser's main thread back for one turn of its event
// loop, so it can draw a frame and run Dart. Go in WebAssembly runs on that
// thread and only returns to the browser when every goroutine is blocked:
// call Yield regularly in long computations. It takes about 0.05 ms, unlike
// time.Sleep, which browsers stretch to 4 ms or more. Natively it does
// nothing. Do not call it from a js.FuncOf callback.
func Yield() {
	wake := make(chan struct{})
	yielding.Lock()
	if yielding.port.IsUndefined() {
		channel := js.Global().Get("MessageChannel").New()
		channel.Get("port1").Set("onmessage", js.FuncOf(func(js.Value, []js.Value) any {
			yielding.Lock()
			next := yielding.waiters[0]
			yielding.waiters = yielding.waiters[1:]
			yielding.Unlock()
			close(next)
			return nil
		}))
		yielding.port = channel.Get("port2")
	}
	yielding.waiters = append(yielding.waiters, wake)
	yielding.port.Call("postMessage", nil)
	yielding.Unlock()
	<-wake
}
