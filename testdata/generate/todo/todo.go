// Package todo is an app that knows nothing about fluffy.
package todo

import "context"

// Item is one thing to do.
type Item struct {
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

// Notifier tells the UI about progress.
//
//fluffy:dart
type Notifier interface {
	Progress(percent float64)
}

type Reader interface {
	// List returns all items.
	List() []Item
}

type Service struct {
	items []Item
	n     Notifier
}

func NewService(n Notifier) *Service { return &Service{n: n} }

// Add adds an item with the title.
func (s *Service) Add(ctx context.Context, title string) (*Item, error) {
	s.items = append(s.items, Item{Title: title})
	return &s.items[len(s.items)-1], nil
}

// List returns all items.
func (s *Service) List() []Item { return s.items }

// Watch sends every item that is added.
func (s *Service) Watch(ctx context.Context) <-chan Item { return nil }

// Each is skipped, funcs cannot be sent.
func (s *Service) Each(fn func(Item)) {}

// Many is skipped, variadic.
func (s *Service) Many(titles ...string) {}

// Version is the app version.
//
//fluffy:export
func Version() string { return "1.0" }
