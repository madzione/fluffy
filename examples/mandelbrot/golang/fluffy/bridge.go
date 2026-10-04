// Package fluffy is the message layer between Go and Dart.
//
// Both sides exchange JSON messages on named channels. Sending goes through
// the emitter (Emit, Request), incoming messages reach the handlers that were
// registered on the receiver (On, Handle, Bind, Export). Dart has the same
// API in lib/golang/, so either side can notify the other or ask it for an
// answer. fluffy generate writes typed Dart functions for the channels it
// finds in the Go code.
//
// The transport underneath is dart:ffi on native platforms (transport_cgo.go)
// and JavaScript globals on the web (transport_js.go). This package is managed
// by fluffy: fluffy init and fluffy update replace it, so changes made here
// are lost.
package fluffy

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
)

// Message types of the wire protocol.
const (
	typeEmit      = "emit"      // one way, no answer
	typeRequest   = "request"   // expects a response or error with the same id
	typeResponse  = "response"  // successful answer to a request
	typeError     = "error"     // failed answer to a request or subscription
	typeSubscribe = "subscribe" // Dart listens to a stream, answered by next, done or error
	typeNext      = "next"      // one value of a stream
	typeDone      = "done"      // the stream has ended
	typeCancel    = "cancel"    // Dart stopped listening to a stream

	// Never sent: fails a Request to a Dart isolate that is gone.
	typeReconnect = "reconnect"
)

type message struct {
	Type    string          `json:"type"`
	ID      int64           `json:"id,omitempty"`
	Channel string          `json:"channel,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// maxQueued limits the messages kept while Dart is not connected yet.
const maxQueued = 1024

var out struct {
	sync.Mutex
	send   func([]byte) // delivers one message to Dart, nil until connected
	conn   atomic.Int64 // counts the connections, changed with the lock held
	queued [][]byte
}

// errReconnected fails what belongs to a Dart isolate that a hot restart
// replaced.
var errReconnected = errors.New("Dart connected again (hot restart)")

// connect sets the function that delivers messages to Dart and flushes
// everything emitted before Dart was listening. Dart connects again after a
// hot restart and numbers its requests and streams from 1 again, so
// everything of the previous Dart isolate ends: its streams are cancelled,
// late answers to its requests are dropped instead of reaching the new
// isolate, and Go's requests to it fail.
func connect(send func([]byte)) {
	begin()
	cancelSubscriptions()
	out.Lock()
	defer out.Unlock()
	reconnect := out.send != nil
	conn := out.conn.Add(1)
	out.send = send
	for _, msg := range out.queued {
		send(msg)
	}
	out.queued = nil
	if reconnect {
		failPending(conn)
	}
}

// post sends msg to Dart, or queues it until Dart connects. conn is the
// connection msg belongs to, 0 for any. A message for an earlier connection
// is dropped with errReconnected.
func post(conn int64, msg message) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	out.Lock()
	defer out.Unlock()
	switch {
	case conn != 0 && conn != out.conn.Load():
		return errReconnected
	case out.send != nil:
		out.send(raw)
	case len(out.queued) < maxQueued:
		out.queued = append(out.queued, raw)
	default:
		log.Printf("fluffy: Dart is not connected, dropped message on %q", msg.Channel)
		return fmt.Errorf("Dart is not connected and %d messages are queued", maxQueued)
	}
	return nil
}

// deliver handles one message from Dart. Answers resolve the waiting Request
// right away, handlers run in their own goroutine so a slow handler neither
// blocks Dart nor other messages.
func deliver(raw []byte) {
	var msg message
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Printf("fluffy: invalid message from Dart: %v", err)
		return
	}
	begin()
	conn := out.conn.Load()
	switch msg.Type {
	case typeResponse, typeError:
		resolve(msg)
	case typeEmit, typeRequest:
		go dispatch(conn, msg)
	case typeSubscribe:
		subscribe(conn, msg)
	case typeCancel:
		unsubscribe(msg.ID)
	default:
		log.Printf("fluffy: unknown message type %q", msg.Type)
	}
}

var startup struct {
	sync.Mutex
	fns   []func() error
	begun bool
	ready chan struct{} // closed once every Start function has returned
	err   error         // the first error of a Start function
}

// Start runs fn once, when Dart connects for the first time. Native builds
// are libraries whose main function never runs, so setup that would be in
// main goes here: open the database, read the config, Bind the services.
//
// Messages from Dart wait until fn has returned, so everything it registers
// exists before the first request. If fn fails, every request is answered
// with its error. Later Start functions run in the order of the calls and
// are skipped after an error. Reconnects after a hot restart do not run fn
// again.
//
// Call Start from an init function, it panics once Dart has connected.
func Start(fn func() error) {
	startup.Lock()
	defer startup.Unlock()
	if startup.begun {
		panic("fluffy: Start called after Dart connected, call it from an init function")
	}
	startup.fns = append(startup.fns, fn)
}

// begin runs the Start functions in the background the first time it is
// called. It never blocks the transport, which runs on Dart's thread.
func begin() {
	startup.Lock()
	defer startup.Unlock()
	if startup.begun {
		return
	}
	startup.begun = true
	ready := make(chan struct{})
	startup.ready = ready
	fns := startup.fns
	go func() {
		defer close(ready)
		for _, fn := range fns {
			if err := runStart(fn); err != nil {
				log.Printf("fluffy: start failed: %v", err)
				startup.Lock()
				startup.err = err
				startup.Unlock()
				return
			}
		}
	}()
}

func runStart(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicError(r)
		}
	}()
	return fn()
}

// started waits for the Start functions and returns their error.
func started() error {
	startup.Lock()
	ready := startup.ready
	startup.Unlock()
	<-ready
	startup.Lock()
	defer startup.Unlock()
	return startup.err
}
