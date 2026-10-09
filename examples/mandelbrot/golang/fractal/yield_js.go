package fractal

import (
	"sync"
	"syscall/js"
)

// yield lets the browser run between tiles. Go in WebAssembly runs on the
// browser's main thread and only returns to it when every goroutine is
// blocked, so without it Flutter would neither see a tile nor draw a frame
// until the whole image is done. It waits for a MessageChannel message,
// about 0.05 ms: time.Sleep would cost 4 ms per tile, browsers throttle
// timeouts set from timeouts. (fluffy.Yield does the same, this package is
// plain Go.)
func yield() {
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

var yielding struct {
	sync.Mutex
	port    js.Value
	waiters []chan struct{}
}
