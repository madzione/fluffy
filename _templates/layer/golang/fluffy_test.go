package fluffy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeDart connects to the layer like Dart does and returns the messages Go
// sends to it.
func fakeDart(t *testing.T) <-chan message {
	t.Helper()
	received := make(chan message, 64)
	connect(func(raw []byte) {
		var msg message
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Errorf("invalid message %s: %v", raw, err)
		}
		// Like the real transports, never block Go: post holds the
		// transport lock. Messages nobody reads, e.g. of an endless stream,
		// are dropped.
		select {
		case received <- msg:
		default:
		}
	})
	// Late messages of a test, e.g. stream values sent before a cancel,
	// are dropped instead of queued for the next test.
	t.Cleanup(func() { connect(func([]byte) {}) })
	return received
}

func disconnect() {
	out.Lock()
	defer out.Unlock()
	out.send = nil
	out.queued = nil
}

func next(t *testing.T, received <-chan message) message {
	t.Helper()
	select {
	case msg := <-received:
		return msg
	case <-time.After(time.Second):
		t.Fatal("no message from Go")
		return message{}
	}
}

func TestHandleAnswersRequests(t *testing.T) {
	received := fakeDart(t)
	Handle("sum", func(req struct{ A, B int }) (int, error) { return req.A + req.B, nil })
	Handle("fail", func(struct{}) (int, error) { return 0, errors.New("broken") })
	Handle("panic", func(struct{}) (int, error) { panic("boom") })

	deliver([]byte(`{"type":"request","id":1,"channel":"sum","data":{"a":2,"b":3}}`))
	if msg := next(t, received); msg.Type != typeResponse || msg.ID != 1 || string(msg.Data) != "5" {
		t.Errorf("sum: %+v", msg)
	}

	for _, channel := range []string{"fail", "panic", "missing"} {
		deliver([]byte(`{"type":"request","id":2,"channel":"` + channel + `"}`))
		if msg := next(t, received); msg.Type != typeError || msg.ID != 2 || msg.Error == "" {
			t.Errorf("%s: %+v", channel, msg)
		}
	}
}

func TestOnReceivesEmits(t *testing.T) {
	got := make(chan string, 1)
	On("log", func(text string) { got <- text })
	deliver([]byte(`{"type":"emit","channel":"log","data":"hi"}`))
	select {
	case text := <-got:
		if text != "hi" {
			t.Errorf("got %q", text)
		}
	case <-time.After(time.Second):
		t.Fatal("handler not called")
	}
}

func TestEmitQueuesUntilConnected(t *testing.T) {
	disconnect()
	if err := Emit("early", 1); err != nil {
		t.Fatal(err)
	}
	received := fakeDart(t)
	if msg := next(t, received); msg.Type != typeEmit || msg.Channel != "early" || string(msg.Data) != "1" {
		t.Errorf("queued: %+v", msg)
	}
}

func TestRequestWaitsForDart(t *testing.T) {
	// Dart answers the first request and fails the second.
	connect(func(raw []byte) {
		var msg message
		json.Unmarshal(raw, &msg)
		answer := message{Type: typeResponse, ID: msg.ID, Data: json.RawMessage(`"yes"`)}
		if msg.ID%2 == 0 {
			answer = message{Type: typeError, ID: msg.ID, Error: "no"}
		}
		reply, _ := json.Marshal(answer)
		go deliver(reply)
	})
	t.Cleanup(disconnect)
	lastID.Store(0)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if answer, err := Request[string](ctx, "confirm", nil); err != nil || answer != "yes" {
		t.Errorf("Request = %q, %v", answer, err)
	}
	var dartErr *DartError
	if _, err := Request[string](ctx, "confirm", nil); !errors.As(err, &dartErr) || dartErr.Message != "no" {
		t.Errorf("Request error = %v", err)
	}
}

type service struct{ base int }

func (s *service) Add(a, b int) int { return s.base + a + b }
func (s *service) Hello(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", errors.New("name is empty")
	}
	return "hi " + name, nil
}
func (s *service) Ping()                      {}
func (s *service) Unsupported(a ...int) int   { return 0 }
func (s *service) TooMany() (int, int, error) { return 0, 0, nil }

type adder interface{ Add(a, b int) int }

func request(t *testing.T, received <-chan message, id int64, channel, data string) message {
	t.Helper()
	deliver([]byte(fmt.Sprintf(`{"type":"request","id":%d,"channel":%q,"data":%s}`, id, channel, data)))
	msg := next(t, received)
	if msg.ID != id {
		t.Fatalf("answer for id %d, want %d", msg.ID, id)
	}
	return msg
}

func TestBind(t *testing.T) {
	received := fakeDart(t)
	Bind("svc", &service{base: 10})
	Bind[adder]("narrow", &service{})

	cases := []struct{ channel, data, want string }{
		{"svc.Add", `[1,2]`, `13`},
		{"svc.Hello", `["Go"]`, `"hi Go"`},
		{"svc.Ping", `[]`, `null`},
		{"svc.Ping", `null`, `null`},
		{"narrow.Add", `[1,2]`, `3`},
	}
	for i, c := range cases {
		if msg := request(t, received, int64(i+1), c.channel, c.data); msg.Type != typeResponse || string(msg.Data) != c.want {
			t.Errorf("%s(%s) = %+v, want %s", c.channel, c.data, msg, c.want)
		}
	}
	for i, c := range []struct{ channel, data string }{
		{"svc.Hello", `[""]`},      // error result
		{"svc.Add", `[1]`},         // wrong number of arguments
		{"svc.Add", `["a","b"]`},   // wrong argument type
		{"svc.Unsupported", `[]`},  // skipped
		{"svc.TooMany", `[]`},      // skipped
		{"narrow.Hello", `["Go"]`}, // not in the interface
	} {
		if msg := request(t, received, int64(100+i), c.channel, c.data); msg.Type != typeError {
			t.Errorf("%s(%s) = %+v, want an error", c.channel, c.data, msg)
		}
	}
}

func TestExport(t *testing.T) {
	received := fakeDart(t)
	Export("mul", func(a, b float64) float64 { return a * b })
	if msg := request(t, received, 1, "mul", `[1.5,2]`); string(msg.Data) != "3" {
		t.Errorf("mul = %+v", msg)
	}
	defer func() {
		if recover() == nil {
			t.Error("Export accepted a function with three results")
		}
	}()
	Export("bad", func() (int, int, int) { return 0, 0, 0 })
}

func TestStream(t *testing.T) {
	received := fakeDart(t)
	stopped := make(chan struct{})
	Export("count", func(ctx context.Context, n int) (<-chan int, error) {
		if n < 0 {
			return nil, errors.New("negative")
		}
		ch := make(chan int)
		go func() {
			defer close(ch)
			for i := 0; n == 0 || i < n; i++ {
				select {
				case ch <- i:
				case <-ctx.Done():
					close(stopped)
					return
				}
			}
		}()
		return ch, nil
	})

	deliver([]byte(`{"type":"subscribe","id":1,"channel":"count","data":[2]}`))
	for _, want := range []string{"0", "1"} {
		if msg := next(t, received); msg.Type != typeNext || msg.ID != 1 || string(msg.Data) != want {
			t.Errorf("next: %+v, want %s", msg, want)
		}
	}
	if msg := next(t, received); msg.Type != typeDone || msg.ID != 1 {
		t.Errorf("done: %+v", msg)
	}

	deliver([]byte(`{"type":"subscribe","id":2,"channel":"count","data":[-1]}`))
	if msg := next(t, received); msg.Type != typeError || msg.ID != 2 {
		t.Errorf("error: %+v", msg)
	}

	if msg := request(t, received, 3, "count", `[1]`); msg.Type != typeError {
		t.Errorf("request on a stream: %+v", msg)
	}

	// An endless stream runs until Dart cancels it.
	deliver([]byte(`{"type":"subscribe","id":4,"channel":"count","data":[0]}`))
	next(t, received)
	deliver([]byte(`{"type":"cancel","id":4}`))
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stream context not cancelled")
	}
}

// The channel of a cancelled stream is drained until it is closed, or idle
// when the sender stopped without closing it.
func TestDrain(t *testing.T) {
	ignoresCtx := make(chan int)
	go func() {
		for i := 0; i < 3; i++ {
			ignoresCtx <- i
		}
		close(ignoresCtx)
	}()
	stopped := make(chan int) // its sender returned without closing it

	for name, ch := range map[string]chan int{"closed": ignoresCtx, "unclosed": stopped} {
		done := make(chan struct{})
		go func() {
			drain(reflect.ValueOf(ch), 20*time.Millisecond)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s: drain did not stop", name)
		}
	}
}

type badJSON struct{}

func (badJSON) MarshalJSON() ([]byte, error) { panic("broken marshaler") }

// A panic while encoding a result fails the call instead of crashing.
func TestMarshalPanic(t *testing.T) {
	received := fakeDart(t)
	Export("bad", func() badJSON { return badJSON{} })
	Export("badStream", func() <-chan badJSON {
		ch := make(chan badJSON, 1)
		ch <- badJSON{}
		close(ch)
		return ch
	})
	if msg := request(t, received, 1, "bad", `[]`); msg.Type != typeError || !strings.Contains(msg.Error, "broken marshaler") {
		t.Errorf("bad = %+v", msg)
	}
	deliver([]byte(`{"type":"subscribe","id":2,"channel":"badStream","data":[]}`))
	if msg := next(t, received); msg.Type != typeError || !strings.Contains(msg.Error, "broken marshaler") {
		t.Errorf("badStream = %+v", msg)
	}
	if err := Emit("bad", badJSON{}); err == nil || !strings.Contains(err.Error(), "broken marshaler") {
		t.Errorf("Emit = %v", err)
	}
}

// After a hot restart the new Dart isolate numbers its requests from 1
// again: answers meant for the old isolate must not reach it.
func TestReconnectDropsOldAnswers(t *testing.T) {
	old := fakeDart(t)
	release := make(chan struct{})
	Export("slow", func() string { <-release; return "old" })
	Export("fast", func() string { return "new" })

	deliver([]byte(`{"type":"request","id":1,"channel":"slow","data":[]}`))
	time.Sleep(10 * time.Millisecond) // the handler is running
	received := fakeDart(t)
	close(release)
	if msg := request(t, received, 1, "fast", `[]`); string(msg.Data) != `"new"` {
		t.Errorf("answer = %+v", msg)
	}
	select {
	case msg := <-received:
		t.Errorf("the new isolate got %+v", msg)
	case msg := <-old:
		t.Errorf("the old isolate got %+v", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

// A request to a Dart isolate that a hot restart replaced fails instead of
// waiting forever.
func TestReconnectFailsRequests(t *testing.T) {
	sent := make(chan struct{}, 1)
	connect(func([]byte) { sent <- struct{}{} }) // never answers
	t.Cleanup(disconnect)

	failed := make(chan error, 1)
	go func() {
		_, err := Request[string](context.Background(), "confirm", nil)
		failed <- err
	}()
	<-sent
	connect(func([]byte) {})
	select {
	case err := <-failed:
		if !errors.Is(err, errReconnected) {
			t.Errorf("Request error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Request still waits after the reconnect")
	}
}

// Requests sent before Dart connects are answered by the first connection.
func TestRequestQueuedBeforeConnect(t *testing.T) {
	disconnect()
	answer := make(chan string, 1)
	go func() {
		got, err := Request[string](context.Background(), "early", nil)
		if err != nil {
			t.Error(err)
		}
		answer <- got
	}()
	for deadline := time.Now().Add(time.Second); ; time.Sleep(time.Millisecond) {
		out.Lock()
		queued := len(out.queued)
		out.Unlock()
		if queued > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request not queued")
		}
	}
	connect(func(raw []byte) {
		var msg message
		json.Unmarshal(raw, &msg)
		reply, _ := json.Marshal(message{Type: typeResponse, ID: msg.ID, Data: json.RawMessage(`"yes"`)})
		go deliver(reply)
	})
	t.Cleanup(disconnect)
	select {
	case got := <-answer:
		if got != "yes" {
			t.Errorf("answer = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no answer")
	}
}

// Start functions run once, before the first message is handled.
func TestStart(t *testing.T) {
	resetStartup(t)
	var calls int
	Start(func() error {
		time.Sleep(10 * time.Millisecond)
		calls++
		Export("late", func() string { return "ready" })
		return nil
	})
	received := fakeDart(t)
	if msg := request(t, received, 1, "late", `[]`); string(msg.Data) != `"ready"` {
		t.Errorf("late = %+v", msg)
	}
	connect(func([]byte) {})
	if calls != 1 {
		t.Errorf("Start function ran %d times", calls)
	}

	resetStartup(t)
	Start(func() error { return errors.New("no database") })
	Start(func() error { t.Error("Start after an error ran"); return nil })
	received = fakeDart(t)
	if msg := request(t, received, 2, "late", `[]`); msg.Type != typeError || !strings.Contains(msg.Error, "no database") {
		t.Errorf("after failed start: %+v", msg)
	}
}

func resetStartup(t *testing.T) {
	t.Helper()
	reset := func() {
		startup.Lock()
		defer startup.Unlock()
		startup.fns = nil
		startup.begun = false
		startup.err = nil
	}
	reset()
	t.Cleanup(func() {
		reset()
		begin()
	})
}
