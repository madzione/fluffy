package todo

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memoryStore struct{ items []Item }

func (m *memoryStore) Load() ([]Item, error)   { return m.items, nil }
func (m *memoryStore) Save(items []Item) error { m.items = items; return nil }

type answer bool

func (a answer) Confirm(context.Context, string) (bool, error) { return bool(a), nil }

func titles(items []Item) []string {
	var out []string
	for _, item := range items {
		out = append(out, item.Title)
	}
	return out
}

func TestService(t *testing.T) {
	store := &memoryStore{}
	s, err := New(store, answer(true))
	if err != nil {
		t.Fatal(err)
	}
	milk, _ := s.Add(" Milk ")
	bread, _ := s.Add("Bread")
	if _, err := s.Add("  "); !errors.Is(err, ErrEmptyTitle) {
		t.Errorf("Add(blank) = %v, want ErrEmptyTitle", err)
	}
	if milk.Title != "Milk" || bread.ID != milk.ID+1 {
		t.Errorf("Add = %+v, %+v", milk, bread)
	}

	done, err := s.Toggle(milk.ID)
	if err != nil || !done.Done || done.DoneAt == nil {
		t.Errorf("Toggle = %+v, %v", done, err)
	}
	if _, err := s.Rename(bread.ID, "Butter"); err != nil {
		t.Error(err)
	}
	if _, err := s.Toggle(42); !errors.Is(err, ErrNotFound) {
		t.Errorf("Toggle(42) = %v, want ErrNotFound", err)
	}

	n, err := s.ClearDone(context.Background())
	if n != 1 || err != nil {
		t.Errorf("ClearDone = %d, %v", n, err)
	}
	if got := titles(store.items); len(got) != 1 || got[0] != "Butter" {
		t.Errorf("saved %v", got)
	}

	// IDs continue after the highest loaded one.
	s, _ = New(store, nil)
	if eggs, _ := s.Add("Eggs"); eggs.ID != bread.ID+1 {
		t.Errorf("ID after reload = %d", eggs.ID)
	}
}

func TestClearDoneDeclined(t *testing.T) {
	s, _ := New(&memoryStore{}, answer(false))
	item, _ := s.Add("Milk")
	s.Toggle(item.ID)
	if n, err := s.ClearDone(context.Background()); n != 0 || err != nil {
		t.Errorf("ClearDone = %d, %v", n, err)
	}
	if len(s.List()) != 1 {
		t.Error("declined ClearDone deleted items")
	}
}

func TestWatch(t *testing.T) {
	s, _ := New(&memoryStore{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	updates := s.Watch(ctx)
	next := func() []Item {
		t.Helper()
		select {
		case items := <-updates:
			return items
		case <-time.After(time.Second):
			t.Fatal("no update")
			return nil
		}
	}
	if items := next(); len(items) != 0 {
		t.Errorf("first update = %v", items)
	}
	s.Add("Milk")
	if items := next(); len(items) != 1 {
		t.Errorf("after Add = %v", items)
	}
	cancel()
	for range updates {
	}
}
