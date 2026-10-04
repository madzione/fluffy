//go:build !js

package todo

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFileStore(t *testing.T) {
	store := FileStore{filepath.Join(t.TempDir(), "dir", "todos.json")}
	if items, err := store.Load(); items != nil || err != nil {
		t.Fatalf("Load of a missing file = %v, %v", items, err)
	}
	want := []Item{{ID: 1, Title: "Milk", Created: time.Now().Round(0)}}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil || len(got) != 1 || !got[0].Created.Equal(want[0].Created) || got[0].Title != "Milk" {
		t.Errorf("Load = %+v, %v", got, err)
	}
}
