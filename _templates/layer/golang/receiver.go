package fluffy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sync"
	"time"
)

// handler gets the raw JSON data of a message and returns the answer for
// requests, or a stream for subscriptions. The answer of an emitted message
// is dropped. ctx ends when Dart cancels a subscription.
type handler func(ctx context.Context, data json.RawMessage) (any, error)

// stream returns the next value of a subscription, ok is false once the
// stream has ended or ctx is done.
type stream func(ctx context.Context) (value any, ok bool)

var handlers sync.Map // channel -> handler

// Handle registers fn to answer Dart requests on channel (emitter.request in
// Dart). The request data is decoded into T, the result is sent back as JSON
// and an error fails the request on the Dart side. Registering a channel
// again replaces its handler.
//
// Handlers run in their own goroutine, register them in an init function so
// they exist before Dart sends the first message.
func Handle[T, R any](channel string, fn func(T) (R, error)) {
	handlers.Store(channel, handler(func(_ context.Context, data json.RawMessage) (any, error) {
		var arg T
		if err := decode(data, &arg); err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
		return fn(arg)
	}))
}

// On registers fn for messages Dart emits on channel (emitter.emit in Dart).
// The data is decoded into T.
func On[T any](channel string, fn func(T)) {
	Handle(channel, func(arg T) (any, error) {
		fn(arg)
		return nil, nil
	})
}

// Remove unregisters the handler of channel.
func Remove(channel string) {
	handlers.Delete(channel)
}

// Bind registers every exported method of T with a supported signature as
// the channel "<namespace>.<Method>", called on impl. Methods with other
// signatures are skipped. To bind only some methods, pass an interface as
// T: Bind[todo.API]("todo", svc) binds the methods of todo.API.
//
// A supported method takes an optional context.Context first, then any
// number of arguments, and returns nothing, R, error, (R, error), or a
// stream: <-chan R or (<-chan R, error). Dart sends the arguments as a JSON
// array. A stream sends every value it receives until the channel is closed;
// its context ends when Dart stops listening, the contexts of other calls
// never end.
//
// Close a stream's channel when it ends, also when it stops because its
// context ended. After a cancel the channel is drained so a sender that
// misses the context is not blocked, but only until the channel is closed or
// idle for a few seconds. Streams have no backpressure: Go sends as fast as
// the channel delivers and Dart buffers what its listener has not handled
// yet, so pace fast producers yourself.
//
// Bind is meant for Start functions, where the services of an app are
// created. fluffy generate writes the typed Dart class for it.
func Bind[T any](namespace string, impl T) {
	v := reflect.ValueOf(&impl).Elem()
	t := v.Type()
	if t.Kind() == reflect.Interface && v.IsNil() {
		panic(fmt.Sprintf("fluffy: Bind %q: impl is nil", namespace))
	}
	for i := 0; i < t.NumMethod(); i++ {
		if method := t.Method(i); method.IsExported() {
			if h, err := funcHandler(v.Method(i)); err == nil {
				handlers.Store(namespace+"."+method.Name, h)
			}
		}
	}
}

// Export registers the function fn as channel, with the signature rules of
// Bind. It panics if fn is not a function with a supported signature.
// fluffy generate registers the functions marked with //fluffy:export this
// way.
func Export(channel string, fn any) {
	h, err := funcHandler(reflect.ValueOf(fn))
	if err != nil {
		panic(fmt.Sprintf("fluffy: Export %q: %v", channel, err))
	}
	handlers.Store(channel, h)
}

var (
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType   = reflect.TypeOf((*error)(nil)).Elem()
)

// funcHandler wraps a function with positional JSON arguments.
func funcHandler(fn reflect.Value) (handler, error) {
	if fn.Kind() != reflect.Func || fn.IsNil() {
		return nil, errors.New("not a function")
	}
	t := fn.Type()
	if t.IsVariadic() {
		return nil, errors.New("variadic functions are not supported")
	}

	withContext := t.NumIn() > 0 && t.In(0) == contextType
	var params []reflect.Type
	for i := 0; i < t.NumIn(); i++ {
		if i > 0 || !withContext {
			params = append(params, t.In(i))
		}
	}

	var hasError, isStream bool
	switch {
	case t.NumOut() == 0:
	case t.NumOut() == 1:
		hasError = t.Out(0) == errorType
		isStream = !hasError && isRecvChan(t.Out(0))
	case t.NumOut() == 2 && t.Out(1) == errorType && t.Out(0) != errorType:
		hasError = true
		isStream = isRecvChan(t.Out(0))
	default:
		return nil, errors.New("results must be nothing, R, error or (R, error)")
	}

	return func(ctx context.Context, data json.RawMessage) (any, error) {
		var raw []json.RawMessage
		if err := decode(data, &raw); err != nil {
			return nil, fmt.Errorf("invalid arguments, want a JSON array: %w", err)
		}
		if len(raw) != len(params) {
			return nil, fmt.Errorf("got %d arguments, want %d", len(raw), len(params))
		}
		args := make([]reflect.Value, 0, t.NumIn())
		if withContext {
			args = append(args, reflect.ValueOf(ctx))
		}
		for i, param := range params {
			arg := reflect.New(param)
			if err := decode(raw[i], arg.Interface()); err != nil {
				return nil, fmt.Errorf("invalid argument %d: %w", i+1, err)
			}
			args = append(args, arg.Elem())
		}

		results := fn.Call(args)
		if hasError {
			if err, _ := results[len(results)-1].Interface().(error); err != nil {
				return nil, err
			}
			results = results[:len(results)-1]
		}
		switch {
		case len(results) == 0:
			return nil, nil
		case isStream:
			return chanStream(results[0]), nil
		default:
			return results[0].Interface(), nil
		}
	}, nil
}

func isRecvChan(t reflect.Type) bool {
	return t.Kind() == reflect.Chan && t.ChanDir()&reflect.RecvDir != 0
}

// chanStream reads a Go channel until it is closed or ctx is done.
func chanStream(ch reflect.Value) stream {
	if ch.IsNil() {
		return func(context.Context) (any, bool) { return nil, false }
	}
	return func(ctx context.Context) (any, bool) {
		chosen, value, ok := reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: ch},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
		})
		if chosen == 0 && ok {
			return value.Interface(), true
		}
		if chosen == 1 {
			go drain(ch, drainIdle)
		}
		return nil, false
	}
}

// drainIdle is how long the channel of a cancelled stream is drained after
// its last value.
const drainIdle = 5 * time.Second

// drain receives from the channel of a cancelled stream, so a sender that
// ignores the context is not blocked forever. It stops when the channel is
// closed, or has been idle for the idle time: a sender that returns on
// ctx.Done without closing the channel would keep it waiting forever.
func drain(ch reflect.Value, idle time.Duration) {
	for {
		timer := time.NewTimer(idle)
		chosen, _, ok := reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: ch},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(timer.C)},
		})
		timer.Stop()
		if chosen == 1 || !ok {
			return
		}
	}
}

// dispatch answers a request or emit that arrived on the connection conn.
func dispatch(conn int64, msg message) {
	result, err := call(context.Background(), msg)
	if _, ok := result.(stream); ok && err == nil {
		err = fmt.Errorf("channel %q is a stream, subscribe to it", msg.Channel)
	}
	if msg.Type != typeRequest {
		if err != nil {
			log.Printf("fluffy: %v", err)
		}
		return
	}

	answer := message{Type: typeResponse, ID: msg.ID}
	if err == nil {
		if answer.Data, err = encode(result); err != nil {
			err = fmt.Errorf("encode the result of %q: %w", msg.Channel, err)
		}
	}
	if err != nil {
		answer = message{Type: typeError, ID: msg.ID, Error: err.Error()}
	}
	// The answer to a Dart isolate that is gone is dropped.
	if err := post(conn, answer); err != nil && !errors.Is(err, errReconnected) {
		log.Printf("fluffy: answer %q: %v", msg.Channel, err)
	}
}

// subscription is one stream Dart listens to.
type subscription struct{ cancel context.CancelFunc }

// Dart numbers subscriptions from 1 again after a hot restart, so the
// entry is compared too, not just the id.
var subscriptions sync.Map // subscription id -> *subscription

// subscribe starts a stream handler for the connection conn. The
// subscription is stored before the handler runs, so a cancel right after
// the subscribe is not lost.
func subscribe(conn int64, msg message) {
	ctx, cancel := context.WithCancel(context.Background())
	sub := &subscription{cancel}
	subscriptions.Store(msg.ID, sub)
	go func() {
		defer func() {
			subscriptions.CompareAndDelete(msg.ID, sub)
			cancel()
		}()
		serve(ctx, conn, msg)
	}()
}

// serve sends the values of a stream to Dart until the stream ends or Dart
// cancels it. Messages for a Dart isolate that is gone are dropped.
func serve(ctx context.Context, conn int64, msg message) {
	fail := func(err error) {
		post(conn, message{Type: typeError, ID: msg.ID, Error: err.Error()})
	}
	result, err := call(ctx, msg)
	if err != nil {
		fail(err)
		return
	}
	next, ok := result.(stream)
	if !ok {
		fail(fmt.Errorf("channel %q is not a stream", msg.Channel))
		return
	}
	for {
		value, ok := next(ctx)
		if !ok {
			break
		}
		data, err := encode(value)
		if err != nil {
			fail(fmt.Errorf("encode %q: %w", msg.Channel, err))
			return
		}
		post(conn, message{Type: typeNext, ID: msg.ID, Data: data})
	}
	if ctx.Err() == nil {
		post(conn, message{Type: typeDone, ID: msg.ID})
	}
}

func unsubscribe(id int64) {
	if sub, ok := subscriptions.LoadAndDelete(id); ok {
		sub.(*subscription).cancel()
	}
}

func cancelSubscriptions() {
	subscriptions.Range(func(id, _ any) bool {
		unsubscribe(id.(int64))
		return true
	})
}

// call runs the handler of msg and turns a panic into an error, so a broken
// handler fails the request instead of crashing the app.
func call(ctx context.Context, msg message) (result any, err error) {
	if err := started(); err != nil {
		return nil, fmt.Errorf("Go failed to start: %w", err)
	}
	h, ok := handlers.Load(msg.Channel)
	if !ok {
		return nil, fmt.Errorf("no Go handler for channel %q", msg.Channel)
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("Go handler %q panicked: %v", msg.Channel, r)
		}
	}()
	return h.(handler)(ctx, msg.Data)
}

func panicError(r any) error {
	if err, ok := r.(error); ok {
		return fmt.Errorf("panic: %w", err)
	}
	return fmt.Errorf("panic: %v", r)
}
