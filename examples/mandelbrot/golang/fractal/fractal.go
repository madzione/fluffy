// Package fractal renders the Mandelbrot set in horizontal bands, one
// worker goroutine per CPU. It is plain Go without fluffy: golang/main.go
// binds a Renderer as "fractal", so Render becomes a Dart stream of tiles
// that Dart cancels when it starts the next render.
package fractal

import (
	"context"
	"errors"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// BandHeight is the height of a tile in pixels.
const BandHeight = 16

// View is the part of the complex plane to render and how.
type View struct {
	// X and Y are the center.
	X float64 `json:"x"`
	Y float64 `json:"y"`
	// Scale is the width of one pixel in the complex plane.
	Scale   float64 `json:"scale"`
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	MaxIter int     `json:"maxIter"`
	// Workers is the number of goroutines, 0 for one per CPU.
	Workers int `json:"workers"`
}

// Tile is a finished band of the image.
type Tile struct {
	Y      int `json:"y"`
	Height int `json:"height"`
	// Pixels are RGBA, Width * Height * 4 bytes.
	Pixels []byte `json:"pixels"`
	// Done counts the tiles sent so far, this one included, of Total.
	Done  int `json:"done"`
	Total int `json:"total"`
	// Micros is the time the worker spent on this tile.
	Micros int64 `json:"micros"`
}

// Stats counts the renders since the start.
type Stats struct {
	Started   int64 `json:"started"`
	Finished  int64 `json:"finished"`
	Cancelled int64 `json:"cancelled"`
	// Workers is the number of worker goroutines running right now.
	Workers int64 `json:"workers"`
}

// Info describes where Go runs.
type Info struct {
	CPUs       int    `json:"cpus"`
	GoMaxProcs int    `json:"goMaxProcs"`
	Platform   string `json:"platform"`
}

// Renderer renders views. The zero value is ready to use.
type Renderer struct {
	started, finished, cancelled, workers atomic.Int64
}

// Info describes the Go runtime: on the web it has one thread, the
// browser's main thread.
func (r *Renderer) Info() Info {
	return Info{CPUs: runtime.NumCPU(), GoMaxProcs: runtime.GOMAXPROCS(0), Platform: runtime.GOOS + "/" + runtime.GOARCH}
}

// Stats returns the counters.
func (r *Renderer) Stats() Stats {
	return Stats{Started: r.started.Load(), Finished: r.finished.Load(), Cancelled: r.cancelled.Load(), Workers: r.workers.Load()}
}

// Render sends the tiles of v in the order they are finished. When ctx
// ends the workers stop after their current row and the channel is closed.
func (r *Renderer) Render(ctx context.Context, v View) (<-chan Tile, error) {
	switch {
	case v.Width < 1 || v.Height < 1 || v.Width > 8192 || v.Height > 8192:
		return nil, errors.New("width and height have to be 1 to 8192")
	case v.MaxIter < 1 || v.MaxIter > 1_000_000:
		return nil, errors.New("maxIter has to be 1 to 1000000")
	case !(v.Scale > 0):
		return nil, errors.New("scale has to be positive")
	}

	total := (v.Height + BandHeight - 1) / BandHeight
	workers := v.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = min(workers, total)

	bands := make(chan int, total)
	for i := 0; i < total; i++ {
		bands <- i
	}
	close(bands)

	r.started.Add(1)
	results := make(chan Tile)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		r.workers.Add(1)
		go func() {
			defer wg.Done()
			defer r.workers.Add(-1)
			for band := range bands {
				tile, ok := renderBand(ctx, v, band)
				if !ok {
					return
				}
				select {
				case results <- tile:
				case <-ctx.Done():
					return
				}
				yield()
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	// Numbers the tiles in the order they are sent and keeps reading
	// results after a cancel, so no worker is left blocked.
	out := make(chan Tile)
	go func() {
		defer close(out)
		done := 0
		for tile := range results {
			done++
			tile.Done, tile.Total = done, total
			select {
			case out <- tile:
			case <-ctx.Done():
			}
		}
		if done == total && ctx.Err() == nil {
			r.finished.Add(1)
		} else {
			r.cancelled.Add(1)
		}
	}()
	return out, nil
}

func renderBand(ctx context.Context, v View, band int) (Tile, bool) {
	start := time.Now()
	y0 := band * BandHeight
	h := min(BandHeight, v.Height-y0)
	pixels := make([]byte, v.Width*h*4)
	for row := 0; row < h; row++ {
		if ctx.Err() != nil {
			return Tile{}, false
		}
		ci := v.Y - (float64(y0+row)-float64(v.Height)/2)*v.Scale
		for col := 0; col < v.Width; col++ {
			cr := v.X + (float64(col)-float64(v.Width)/2)*v.Scale
			i := (row*v.Width + col) * 4
			pixels[i], pixels[i+1], pixels[i+2] = color(escape(cr, ci, v.MaxIter))
			pixels[i+3] = 255
		}
	}
	return Tile{Y: y0, Height: h, Pixels: pixels, Micros: time.Since(start).Microseconds()}, true
}

// escape returns the smooth iteration count at which c escapes, or -1 if
// it stays bounded for maxIter iterations.
func escape(cr, ci float64, maxIter int) float64 {
	// Inside the main cardioid or the period-2 bulb.
	q := (cr-0.25)*(cr-0.25) + ci*ci
	if q*(q+cr-0.25) <= 0.25*ci*ci || (cr+1)*(cr+1)+ci*ci <= 1.0/16 {
		return -1
	}
	var zr, zi, zr2, zi2 float64
	for i := 0; i < maxIter; i++ {
		zi = 2*zr*zi + ci
		zr = zr2 - zi2 + cr
		zr2, zi2 = zr*zr, zi*zi
		if zr2+zi2 > 256 {
			return float64(i) + 1 - math.Log2(math.Log(zr2+zi2)/2)
		}
	}
	return -1
}

// color maps a smooth iteration count to a cosine palette, the inside is
// black.
func color(n float64) (r, g, b byte) {
	if n < 0 {
		return 0, 0, 0
	}
	t := math.Sqrt(n) * 0.25
	channel := func(phase float64) byte {
		return byte(255 * (0.5 + 0.5*math.Cos(2*math.Pi*(t+phase))))
	}
	return channel(0.55), channel(0.65), channel(0.8)
}
