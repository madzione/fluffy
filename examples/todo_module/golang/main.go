package main

import (
	"example.com/todo"
	"todo_module/golang/fluffy"
)

//go:generate fluffy generate

// The todo service is its own Go module in ../todo, required in go.mod
// with a replace to its local path, so fluffy generate scans it. It is plain
// Go: it doesn't import fluffy and is tested with go test. Here it is created
// once and its methods are bound as golang.todo.<method> in Dart. newTodoUI
// is generated from the //fluffy:dart interface todo.UI and forwards to the
// Dart implementation.
func init() {
	fluffy.Start(func() error {
		store, err := todo.DefaultStore()
		if err != nil {
			return err
		}
		svc, err := todo.New(store, newTodoUI())
		if err != nil {
			return err
		}
		fluffy.Bind("todo", svc)
		return nil
	})
}

// main only runs on the web, native platforms load golang/ as a library.
// Setup that would go here belongs in a fluffy.Start function.
func main() {
	fluffy.Serve()
}
