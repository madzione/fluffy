package main

import (
	"mandelbrot/golang/bench"
	"mandelbrot/golang/fluffy"
	"mandelbrot/golang/fractal"
)

//go:generate fluffy generate

// The renderer (golang/fractal) and the benchmarks (golang/bench) are plain
// Go, bound here as golang.fractal.* and golang.bench.* in Dart.
func init() {
	fluffy.Start(func() error {
		fluffy.Bind("fractal", &fractal.Renderer{})
		fluffy.Bind("bench", bench.Bench{})
		return nil
	})
}

// main only runs on the web, native platforms load golang/ as a library.
// Setup that would go here belongs in a fluffy.Start function.
func main() {
	fluffy.Serve()
}
