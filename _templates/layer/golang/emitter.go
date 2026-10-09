package fluffy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// DartError is returned by Request when the Dart handler failed.
type DartError struct {
	Channel string
	Message string
}

func (e *DartError) Error() string {
	return fmt.Sprintf("dart handler %q: %s", e.Channel, e.Message)
}

var (
	lastID  atomic.Int64
	pending sync.Map // request id -> *waiter
)

// waiter is a Request waiting for its answer.
type waiter struct {
	answer chan message // gets exactly one message, see resolve
	conn   int64        // the connection the request went to
}

// Emit sends data to the Dart listeners of channel (receiver.on in Dart)
// without waiting for an answer. data is encoded as JSON. Messages emitted
// before Dart connects are queued, up to a limit.
func Emit(channel string, data any) error {
	raw, blobs, err := encodeData(data)
	if err != nil {
		return fmt.Errorf("fluffy: encode %q: %w", channel, err)
	}
	if _, err := post(0, message{Type: typeEmit, Channel: channel, Data: raw, blobs: blobs}); err != nil {
		return fmt.Errorf("fluffy: emit %q: %w", channel, err)
	}
	return nil
}

// Request sends data to the Dart handler of channel (receiver.handle in
// Dart) and waits for its answer, decoded into R. Use the context to limit
// how long to wait. The request fails when Dart connects again (hot
// restart) before it has answered.
func Request[R any](ctx context.Context, channel string, data any) (R, error) {
	var result R
	raw, blobs, err := encodeData(data)
	if err != nil {
		return result, fmt.Errorf("fluffy: encode %q: %w", channel, err)
	}

	id := lastID.Add(1)
	w := &waiter{answer: make(chan message, 1), conn: out.conn.Load()}
	pending.Store(id, w)
	defer pending.Delete(id)

	if _, err := post(w.conn, message{Type: typeRequest, ID: id, Channel: channel, Data: raw, blobs: blobs}); err != nil {
		return result, fmt.Errorf("fluffy: request %q: %w", channel, err)
	}
	select {
	case msg := <-w.answer:
		switch msg.Type {
		case typeError:
			return result, &DartError{Channel: channel, Message: msg.Error}
		case typeReconnect:
			return result, fmt.Errorf("fluffy: request %q: %w", channel, errReconnected)
		}
		if err := decode(msg.Data, msg.blobs, &result); err != nil {
			return result, fmt.Errorf("fluffy: decode answer of %q: %w", channel, err)
		}
		return result, nil
	case <-ctx.Done():
		return result, errors.Join(fmt.Errorf("fluffy: request %q", channel), ctx.Err())
	}
}

// resolve hands an answer from Dart to the waiting Request.
func resolve(msg message) {
	if w, ok := pending.LoadAndDelete(msg.ID); ok {
		w.(*waiter).answer <- msg
	}
}

// failPending fails the Requests sent before the connection conn: the Dart
// isolate they went to is gone and will never answer. Requests queued
// before the first connection went to the current one.
func failPending(conn int64) {
	pending.Range(func(id, w any) bool {
		if w.(*waiter).conn < conn && pending.CompareAndDelete(id, w) {
			w.(*waiter).answer <- message{Type: typeReconnect}
		}
		return true
	})
}
