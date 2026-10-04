package fractal

import "time"

// yield lets the browser run between tiles. Go in WebAssembly runs on the
// browser's main thread and only returns to it when every goroutine is
// blocked, so without the sleep Flutter would neither see a tile nor draw a
// frame until the whole image is done.
func yield() { time.Sleep(time.Millisecond) }
