package main

import (
	"context"
	"errors"
	"time"

	"demo/golang/fluffy"
	"example.com/todo"
)

type addRequest struct {
	A int64 `json:"a"`
	B int64 `json:"b"`
}

// User is someone using the app.
type User struct {
	Base
	*Audit

	// Name is shown in the UI.
	Name    string             `json:"name"`
	Age     int                `json:"age,omitempty"`
	Avatar  []byte             `json:"avatar"`
	Tags    []string           `json:"tags"`
	Scores  map[string]float64 `json:"scores"`
	Friends []*User            `json:"friends"`
	Born    time.Time          `json:"born"`
	Timeout time.Duration      `json:"timeout"`
	Extra   any                `json:"extra"`
	Count   int64              `json:"count,string"`
	ByID    map[int]todo.Item
	Skipped string `json:"-"`
	hidden  int
}

type Base struct {
	ID string `json:"id"`
}

type Audit struct {
	Created time.Time `json:"created"`
}

// Add adds a and b.
//
//fluffy:export
func Add(a, b int64) int64 { return a + b }

// FindUser looks a user up.
//
//fluffy:export
func FindUser(ctx context.Context, id string) (*User, error) {
	if id == "" {
		return nil, errors.New("no id")
	}
	return &User{Name: id}, nil
}

//fluffy:export
func Clock(ctx context.Context) <-chan string { return nil }

//fluffy:export
func Reset() error { return nil }

// Dart is implemented in Dart.
//
//fluffy:dart
type Dart interface {
	// Confirm asks the user.
	Confirm(ctx context.Context, question string) (bool, error)
	Progress(percent float64)
	Saved(user User) error
}

func init() {
	// Sums two numbers.
	fluffy.Handle("sum", func(req addRequest) (int64, error) { return req.A + req.B, nil })
	fluffy.Handle("ping", func(struct{}) (struct{}, error) { return struct{}{}, nil })
	fluffy.Handle("echo", func(text string) (string, error) { return text, nil })
	fluffy.On("log", func(line string) {})

	fluffy.Start(func() error {
		fluffy.Bind("todo", todo.NewService(newTodoNotifier()))
		fluffy.Bind[todo.Reader]("reader", todo.NewService(nil))
		return nil
	})

	go func() {
		fluffy.Emit("tick", time.Now())
		fluffy.Emit("users", []User{})
		fluffy.Emit("ready", nil)
		fluffy.Request[bool](context.Background(), "confirm", "Sure?")
		_ = newDart()
	}()
}

// Counts has fields named like the Dart types the generated code uses.
type Counts struct {
	Int    int     `json:"int"`
	Bool   bool    `json:"bool"`
	Double float64 `json:"double"`
	// Seen is a pointer that toJson leaves out when it is null.
	Seen *time.Time `json:"seen,omitempty"`
}

//fluffy:export
func Int(int int, emitter string) Counts { return Counts{Int: int} }
