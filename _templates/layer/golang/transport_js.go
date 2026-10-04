//go:build js && wasm

package fluffy

import "syscall/js"

// Serve registers the web transport on globalThis.golang and keeps the Go
// runtime alive. Dart passes a function to golang.connect that receives each
// message as a string, and sends with golang.send. Then it calls
// globalThis.fluffyReady, which the loader in web/index.html waits for
// before it starts Flutter.
func Serve() {
	js.Global().Set("golang", js.ValueOf(map[string]any{
		"connect": js.FuncOf(func(this js.Value, args []js.Value) any {
			callback := args[0]
			connect(func(msg []byte) { callback.Invoke(string(msg)) })
			return nil
		}),
		"send": js.FuncOf(func(this js.Value, args []js.Value) any {
			deliver([]byte(args[0].String()))
			return nil
		}),
	}))
	if ready := js.Global().Get("fluffyReady"); ready.Type() == js.TypeFunction {
		ready.Invoke()
	}
	select {}
}
