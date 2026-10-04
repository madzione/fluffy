// Package todo is the todo list, a Go module of its own. It knows nothing
// about Flutter or fluffy, golang/main.go of the Flutter app binds a Service
// to Dart.
package todo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// Item is one thing to do.
type Item struct {
	ID      int64     `json:"id"`
	Title   string    `json:"title"`
	Done    bool      `json:"done"`
	Created time.Time `json:"created"`
	// DoneAt is when the item was done, nil while it is open.
	DoneAt *time.Time `json:"doneAt,omitempty"`
}

// UI is what the service needs from its user interface.
//
//fluffy:dart
type UI interface {
	// Confirm asks the user a yes/no question.
	Confirm(ctx context.Context, question string) (bool, error)
}

// Store keeps the items between runs.
type Store interface {
	Load() ([]Item, error)
	Save(items []Item) error
}

var (
	ErrEmptyTitle = errors.New("the title is empty")
	ErrNotFound   = errors.New("no such item")
)

// Service is the todo list. It is safe for concurrent use.
type Service struct {
	store Store
	ui    UI

	mu       sync.Mutex
	items    []Item
	nextID   int64
	watchers map[chan struct{}]struct{}
}

// New loads the items from store. ui may be nil, then nothing is confirmed.
func New(store Store, ui UI) (*Service, error) {
	items, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}
	s := &Service{store: store, ui: ui, items: items, nextID: 1, watchers: map[chan struct{}]struct{}{}}
	for _, item := range items {
		s.nextID = max(s.nextID, item.ID+1)
	}
	return s, nil
}

// List returns all items, oldest first.
func (s *Service) List() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.items)
}

// Add adds an open item with the title.
func (s *Service) Add(title string) (Item, error) {
	title, err := cleanTitle(title)
	if err != nil {
		return Item{}, err
	}
	var item Item
	err = s.update(func(items []Item) ([]Item, error) {
		item = Item{ID: s.nextID, Title: title, Created: time.Now()}
		s.nextID++
		return append(items, item), nil
	})
	return item, err
}

// Rename changes the title of the item with the id.
func (s *Service) Rename(id int64, title string) (Item, error) {
	title, err := cleanTitle(title)
	if err != nil {
		return Item{}, err
	}
	return s.change(id, func(item *Item) { item.Title = title })
}

// Toggle marks an open item as done and a done item as open.
func (s *Service) Toggle(id int64) (Item, error) {
	return s.change(id, func(item *Item) {
		item.Done = !item.Done
		item.DoneAt = nil
		if item.Done {
			now := time.Now()
			item.DoneAt = &now
		}
	})
}

// Remove deletes the item with the id.
func (s *Service) Remove(id int64) error {
	return s.update(func(items []Item) ([]Item, error) {
		i := slices.IndexFunc(items, func(item Item) bool { return item.ID == id })
		if i < 0 {
			return nil, ErrNotFound
		}
		return slices.Delete(items, i, i+1), nil
	})
}

// ClearDone deletes all done items after the UI confirmed it and returns
// how many it deleted.
func (s *Service) ClearDone(ctx context.Context) (int, error) {
	done := 0
	for _, item := range s.List() {
		if item.Done {
			done++
		}
	}
	if done == 0 {
		return 0, nil
	}
	if s.ui != nil {
		ok, err := s.ui.Confirm(ctx, fmt.Sprintf("Delete %d done item(s)?", done))
		if err != nil || !ok {
			return 0, err
		}
	}
	removed := 0
	err := s.update(func(items []Item) ([]Item, error) {
		kept := slices.DeleteFunc(items, func(item Item) bool { return item.Done })
		removed = len(items) - len(kept)
		return kept, nil
	})
	return removed, err
}

// Watch sends the items now and after every change until ctx ends. A slow
// receiver only gets the latest list.
func (s *Service) Watch(ctx context.Context) <-chan []Item {
	out := make(chan []Item)
	changed := make(chan struct{}, 1)
	changed <- struct{}{}
	s.mu.Lock()
	s.watchers[changed] = struct{}{}
	s.mu.Unlock()
	go func() {
		defer close(out)
		defer func() {
			s.mu.Lock()
			delete(s.watchers, changed)
			s.mu.Unlock()
		}()
		for {
			select {
			case <-changed:
				select {
				case out <- s.List():
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// change applies fn to the item with the id and returns the changed item.
func (s *Service) change(id int64, fn func(item *Item)) (Item, error) {
	var changed Item
	err := s.update(func(items []Item) ([]Item, error) {
		i := slices.IndexFunc(items, func(item Item) bool { return item.ID == id })
		if i < 0 {
			return nil, ErrNotFound
		}
		fn(&items[i])
		changed = items[i]
		return items, nil
	})
	return changed, err
}

// update saves the items fn returns for a copy of the current ones and
// tells the watchers. Nothing changes when fn or saving fails.
func (s *Service) update(fn func(items []Item) ([]Item, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := fn(slices.Clone(s.items))
	if err != nil {
		return err
	}
	if err := s.store.Save(items); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	s.items = items
	for w := range s.watchers {
		select {
		case w <- struct{}{}:
		default: // already pending
		}
	}
	return nil
}

func cleanTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", ErrEmptyTitle
	}
	return title, nil
}

// Version is the version of the todo app.
//
//fluffy:export
func Version() string { return "1.0.0" }
