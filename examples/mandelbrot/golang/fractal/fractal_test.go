package fractal

import (
	"context"
	"testing"
	"time"
)

func TestRender(t *testing.T) {
	var r Renderer
	v := View{X: -0.5, Scale: 3.0 / 200, Width: 200, Height: 100, MaxIter: 200}
	tiles, err := r.Render(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]bool, v.Height)
	n := 0
	for tile := range tiles {
		n++
		if tile.Done != n || tile.Total != 7 {
			t.Errorf("tile %d: done %d of %d, want %d of 7", n, tile.Done, tile.Total, n)
		}
		if len(tile.Pixels) != v.Width*tile.Height*4 {
			t.Errorf("tile %d has %d bytes", n, len(tile.Pixels))
		}
		for y := tile.Y; y < tile.Y+tile.Height; y++ {
			rows[y] = true
		}
	}
	for y, ok := range rows {
		if !ok {
			t.Errorf("row %d is missing", y)
		}
	}
	if s := r.Stats(); s != (Stats{Started: 1, Finished: 1}) {
		t.Errorf("stats = %+v", s)
	}
}

func TestCancel(t *testing.T) {
	var r Renderer
	ctx, cancel := context.WithCancel(context.Background())
	tiles, err := r.Render(ctx, View{X: -0.75, Y: 0.1, Scale: 1e-5, Width: 2000, Height: 2000, MaxIter: 100000, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	<-tiles
	cancel()
	for range tiles {
	}
	deadline := time.Now().Add(5 * time.Second)
	for r.Stats() != (Stats{Started: 1, Cancelled: 1}) {
		if time.Now().After(deadline) {
			t.Fatalf("stats = %+v", r.Stats())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRenderRejects(t *testing.T) {
	var r Renderer
	for _, v := range []View{{Width: 0, Height: 1, Scale: 1, MaxIter: 1}, {Width: 1, Height: 1, MaxIter: 1}} {
		if _, err := r.Render(context.Background(), v); err == nil {
			t.Errorf("%+v: no error", v)
		}
	}
}
