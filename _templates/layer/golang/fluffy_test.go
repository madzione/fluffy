package fluffy

import (
	"bytes"
	"context"
	"encoding/binary"
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
	connectDart(t, func(msg message) {
		// Messages nobody reads, e.g. of an endless stream, are dropped.
		select {
		case received <- msg:
		default:
		}
	})
	return received
}

// connectDart connects a Dart that takes the outbox when Go rings and calls
// handle with each message, in a goroutine of its own like Dart's event
// loop.
func connectDart(t *testing.T, handle func(message)) {
	t.Helper()
	rings := make(chan struct{}, 1)
	stop := make(chan struct{})
	connect(func() {
		select {
		case rings <- struct{}{}:
		default:
		}
	})
	go func() {
		for {
			select {
			case <-rings:
			case <-stop:
				return
			}
			for _, msg := range takeMessages(t) {
				handle(msg)
			}
		}
	}()
	// Late messages of a test, e.g. stream values sent before a cancel,
	// are dropped instead of queued for the next test.
	t.Cleanup(func() {
		close(stop)
		connect(func() {})
	})
}

// takeMessages takes the outbox and decodes its frames.
func takeMessages(t *testing.T) []message {
	segments, _ := take()
	var raw []byte
	for _, s := range segments {
		raw = append(raw, s...)
	}
	var msgs []message
	for len(raw) > 0 {
		msg, rest, err := readFrame(raw)
		if err != nil {
			t.Errorf("invalid frame: %v", err)
			return msgs
		}
		msgs = append(msgs, msg)
		raw = rest
	}
	return msgs
}

func disconnect() {
	out.Lock()
	defer out.Unlock()
	out.ring, out.push = nil, nil
	out.segments, out.size, out.queued = nil, 0, 0
}

// frameOf builds the frame Dart sends for the JSON envelope s and the
// blobs, s has their paths in bin already.
func frameOf(s string, blobs ...[]byte) []byte {
	raw := binary.LittleEndian.AppendUint32(nil, uint32(len(s)))
	raw = binary.LittleEndian.AppendUint32(raw, uint32(len(blobs)))
	raw = append(raw, s...)
	for _, b := range blobs {
		raw = binary.LittleEndian.AppendUint32(raw, uint32(len(b)))
		raw = append(raw, b...)
	}
	return raw
}

// send delivers the JSON envelope s from Dart.
func send(s string, blobs ...[]byte) *answer {
	return deliver(frameOf(s, blobs...), nil)
}

// answerTo replies to every request from Go with answer.
func answerTo(t *testing.T, answer func(message) message) {
	connectDart(t, func(msg message) {
		if msg.Type == typeRequest {
			reply := answer(msg)
			reply.ID = msg.ID
			go deliverMessage(reply)
		}
	})
}

// deliverMessage delivers msg from Dart.
func deliverMessage(msg message) {
	segments, _ := msg.frame()
	var raw []byte
	for _, s := range segments {
		raw = append(raw, s...)
	}
	deliver(raw, nil)
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

	send(`{"type":"request","id":1,"channel":"sum","data":{"a":2,"b":3}}`)
	if msg := next(t, received); msg.Type != typeResponse || msg.ID != 1 || string(msg.Data) != "5" {
		t.Errorf("sum: %+v", msg)
	}

	for _, channel := range []string{"fail", "panic", "missing"} {
		send(`{"type":"request","id":2,"channel":"` + channel + `"}`)
		if msg := next(t, received); msg.Type != typeError || msg.ID != 2 || msg.Error == "" {
			t.Errorf("%s: %+v", channel, msg)
		}
	}
}

func TestOnReceivesEmits(t *testing.T) {
	got := make(chan string, 1)
	On("log", func(text string) { got <- text })
	send(`{"type":"emit","channel":"log","data":"hi"}`)
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
	answerTo(t, func(msg message) message {
		if msg.ID%2 == 0 {
			return message{Type: typeError, Error: "no"}
		}
		return message{Type: typeResponse, Data: json.RawMessage(`"yes"`)}
	})
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
	send(fmt.Sprintf(`{"type":"request","id":%d,"channel":%q,"data":%s}`, id, channel, data))
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

	send(`{"type":"subscribe","id":1,"channel":"count","data":[2]}`)
	for _, want := range []string{"0", "1"} {
		if msg := next(t, received); msg.Type != typeNext || msg.ID != 1 || string(msg.Data) != want {
			t.Errorf("next: %+v, want %s", msg, want)
		}
	}
	if msg := next(t, received); msg.Type != typeDone || msg.ID != 1 {
		t.Errorf("done: %+v", msg)
	}

	send(`{"type":"subscribe","id":2,"channel":"count","data":[-1]}`)
	if msg := next(t, received); msg.Type != typeError || msg.ID != 2 {
		t.Errorf("error: %+v", msg)
	}

	if msg := request(t, received, 3, "count", `[1]`); msg.Type != typeError {
		t.Errorf("request on a stream: %+v", msg)
	}

	// An endless stream runs until Dart cancels it.
	send(`{"type":"subscribe","id":4,"channel":"count","data":[0]}`)
	next(t, received)
	send(`{"type":"cancel","id":4}`)
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
	send(`{"type":"subscribe","id":2,"channel":"badStream","data":[]}`)
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

	send(`{"type":"request","id":1,"channel":"slow","data":[]}`)
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
	connect(func() { sent <- struct{}{} }) // never answers
	t.Cleanup(disconnect)

	failed := make(chan error, 1)
	go func() {
		_, err := Request[string](context.Background(), "confirm", nil)
		failed <- err
	}()
	<-sent
	connect(func() {})
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
		queued := out.queued
		out.Unlock()
		if queued > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request not queued")
		}
	}
	answerTo(t, func(message) message {
		return message{Type: typeResponse, Data: json.RawMessage(`"yes"`)}
	})
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
	connect(func() {})
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

func TestFrame(t *testing.T) {
	msg := message{Type: typeNext, ID: 7, Channel: `a"b`, Data: json.RawMessage(`{"x":null}`), Error: "<e>",
		blobs: []blob{{data: []byte{1, 2}}, {path: []any{"x"}, data: nil}}}
	segments, size := msg.frame()
	var raw []byte
	for _, s := range segments {
		raw = append(raw, s...)
	}
	if len(raw) != size {
		t.Fatalf("size %d, frame has %d bytes", size, len(raw))
	}
	got, rest, err := readFrame(append(raw, 9))
	if err != nil || !bytes.Equal(rest, []byte{9}) {
		t.Fatalf("readFrame: %v, rest %v", err, rest)
	}
	if got.Type != msg.Type || got.ID != msg.ID || got.Channel != msg.Channel || string(got.Data) != string(msg.Data) || got.Error != msg.Error {
		t.Errorf("got %+v, want %+v", got, msg)
	}
	if len(got.blobs) != 2 || !bytes.Equal(got.blobs[0].data, []byte{1, 2}) || len(got.blobs[0].path) != 0 ||
		len(got.blobs[1].data) != 0 || got.blobs[1].path[0] != "x" {
		t.Errorf("blobs %+v", got.blobs)
	}
	for _, bad := range [][]byte{raw[:5], raw[:len(raw)-1], frameOf(`{"type":"emit"}`, []byte{1})} {
		if _, _, err := readFrame(bad); err == nil {
			t.Errorf("readFrame(%v) accepted a broken frame", bad)
		}
	}
}

type tile struct {
	Y      int    `json:"y"`
	Pixels []byte `json:"pixels"`
	Mask   []byte `json:"mask,omitempty"`
	Small  []byte
	Ptr    *[]byte `json:"ptr"`
}

type embeds struct {
	tile
	Extra []byte `json:"extra"`
}

type marshals struct{ Data []byte }

func (m marshals) MarshalJSON() ([]byte, error) { return []byte(`"custom"`), nil }

type textBytes []byte

func (textBytes) MarshalText() ([]byte, error) { return []byte("text"), nil }

func TestEncodeData(t *testing.T) {
	ptr := []byte{9}
	cases := []struct {
		name  string
		value any
		data  string
		paths []string
	}{
		{"bytes", []byte{1, 2, 3}, ``, []string{`[]`}},
		{"empty bytes", []byte{}, `""`, nil},
		{"nil bytes", []byte(nil), `null`, nil},
		{"text bytes", textBytes{1}, `"text"`, nil},
		{"struct", tile{Y: 1, Pixels: []byte{1}, Mask: []byte{2}, Ptr: &ptr}, `{"y":1,"pixels":null,"Small":null,"ptr":"CQ=="}`, []string{`["pixels"]`, `["mask"]`}},
		{"pointer", &tile{Small: []byte{1}}, `{"y":0,"pixels":null,"Small":null,"ptr":null}`, []string{`["Small"]`}},
		{"no blobs", tile{Y: 2}, `{"y":2,"pixels":null,"Small":null,"ptr":null}`, nil},
		{"embedded", embeds{Extra: []byte{1}}, `{"y":0,"pixels":null,"Small":null,"ptr":null,"extra":"AQ=="}`, nil},
		{"marshaler", marshals{[]byte{1}}, `"custom"`, nil},
		{"nested", []tile{{Pixels: []byte{1}}}, `[{"y":0,"pixels":"AQ==","Small":null,"ptr":null}]`, nil},
	}
	for _, c := range cases {
		data, blobs, err := encodeData(c.value)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		var paths []string
		for _, b := range blobs {
			raw, _ := json.Marshal(b.path)
			if b.path == nil {
				raw = []byte("[]")
			}
			paths = append(paths, string(raw))
		}
		if string(data) != c.data || fmt.Sprint(paths) != fmt.Sprint(c.paths) {
			t.Errorf("%s: %s %v, want %s %v", c.name, data, paths, c.data, c.paths)
		}
	}
	// The value sent is not changed.
	v := tile{Pixels: []byte{1}}
	encodeData(v)
	encodeData(&v)
	if v.Pixels == nil {
		t.Error("encodeData changed the value")
	}
}

func TestDecodeBlobs(t *testing.T) {
	b := []byte{1, 2, 3}
	var bs []byte
	if err := decode(nil, []blob{{data: b}}, &bs); err != nil || !bytes.Equal(bs, b) {
		t.Errorf("bytes: %v %v", bs, err)
	}
	var tl *tile
	if err := decode(json.RawMessage(`{"y":4,"pixels":null}`), []blob{{path: []any{"pixels"}, data: b}, {path: []any{"ptr"}, data: b}}, &tl); err != nil ||
		tl.Y != 4 || !bytes.Equal(tl.Pixels, b) || !bytes.Equal(*tl.Ptr, b) {
		t.Errorf("struct: %+v %v", tl, err)
	}
	// Elsewhere the blob becomes base64 for encoding/json.
	var anything any
	if err := decode(nil, []blob{{data: b}}, &anything); err != nil || anything != "AQID" {
		t.Errorf("any: %v %v", anything, err)
	}
	var m map[string][]byte
	if err := decode(json.RawMessage(`{"k":null}`), []blob{{path: []any{"k"}, data: b}}, &m); err != nil || !bytes.Equal(m["k"], b) {
		t.Errorf("map: %v %v", m, err)
	}
	var e embeds
	if err := decode(json.RawMessage(`{"extra":null}`), []blob{{path: []any{"extra"}, data: b}}, &e); err != nil || !bytes.Equal(e.Extra, b) {
		t.Errorf("embedded: %+v %v", e, err)
	}
	if err := decode(json.RawMessage(`{}`), []blob{{path: []any{"a", "b"}, data: b}}, &m); err == nil {
		t.Error("a blob path that does not exist was accepted")
	}
}

func TestBlobsOnTheWire(t *testing.T) {
	received := fakeDart(t)
	Export("take", func(b []byte, tl tile) int { return len(b) + len(tl.Pixels) })
	Export("bytes", func(n int) []byte { return bytes.Repeat([]byte{7}, n) })
	Export("tile", func() *tile { return &tile{Y: 3, Pixels: []byte{1, 2}} })

	send(`{"type":"request","id":1,"channel":"take","data":[null,{"pixels":null}],"bin":[[0],[1,"pixels"]]}`, []byte{1, 2, 3}, []byte{4})
	if msg := next(t, received); string(msg.Data) != "4" {
		t.Errorf("take = %+v", msg)
	}
	send(`{"type":"request","id":2,"channel":"bytes","data":[5]}`)
	if msg := next(t, received); len(msg.Data) != 0 || len(msg.blobs) != 1 || !bytes.Equal(msg.blobs[0].data, []byte{7, 7, 7, 7, 7}) {
		t.Errorf("bytes = %+v", msg)
	}
	send(`{"type":"request","id":3,"channel":"tile","data":[]}`)
	if msg := next(t, received); string(msg.Data) != `{"y":3,"pixels":null,"Small":null,"ptr":null}` || len(msg.blobs) != 1 || msg.blobs[0].path[0] != "pixels" {
		t.Errorf("tile = %+v", msg)
	}
	send(`{"type":"request","id":4,"channel":"take","data":[null,{}],"bin":[[]]}`, []byte{1})
	if msg := next(t, received); msg.Type != typeError {
		t.Errorf("bytes instead of arguments = %+v", msg)
	}
}

// A request waits for its answer on the sending thread, a slow channel
// stops waiting until it is fast again.
func TestAwait(t *testing.T) {
	received := make(chan message, 64)
	disconnect()
	connect(func() {}) // the doorbell is ignored, take only after await
	t.Cleanup(func() { connect(func() {}) })

	var delay time.Duration
	Export("maybeSlow", func() string { time.Sleep(delay); return "ok" })
	takeAll := func() {
		for _, msg := range takeMessages(t) {
			received <- msg
		}
	}

	a := send(`{"type":"request","id":1,"channel":"maybeSlow","data":[]}`)
	if a == nil || !await(a) {
		t.Fatal("fast request was not answered within syncWait")
	}
	takeAll()
	if msg := next(t, received); string(msg.Data) != `"ok"` {
		t.Errorf("answer = %+v", msg)
	}

	delay = 5 * syncWait
	if a := send(`{"type":"request","id":2,"channel":"maybeSlow","data":[]}`); await(a) {
		t.Error("slow request reported an answer")
	}
	if a := send(`{"type":"request","id":3,"channel":"maybeSlow","data":[]}`); a != nil {
		t.Error("send waits for a slow channel")
	}
	time.Sleep(3 * delay)
	takeAll()
	next(t, received)
	next(t, received)

	// Once it is fast again, sends wait again.
	delay = 0
	send(`{"type":"request","id":4,"channel":"maybeSlow","data":[]}`)
	time.Sleep(10 * time.Millisecond)
	if a := send(`{"type":"request","id":5,"channel":"maybeSlow","data":[]}`); a == nil || !await(a) {
		t.Error("send does not wait for a channel that is fast again")
	}
}

// Large frames are decoded by the reader goroutine, later small ones still
// come after them.
func TestLargeFramesKeepOrder(t *testing.T) {
	fakeDart(t)
	stopped := make(chan struct{})
	Export("big", func(ctx context.Context, pad string) <-chan int {
		go func() {
			<-ctx.Done()
			close(stopped)
		}()
		return make(chan int)
	})
	pad := strings.Repeat("x", 2*inlineJSON)
	if a := send(`{"type":"subscribe","id":1,"channel":"big","data":["` + pad + `"]}`); a != nil {
		t.Error("a large frame was handled inline")
	}
	if a := send(`{"type":"emit","channel":"none"}`, make([]byte, 2*inlineSize)); a != nil {
		t.Error("a large frame was handled inline")
	}
	send(`{"type":"cancel","id":1}`)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("the cancel overtook the subscribe")
	}
}

// Go stops sending stream values Dart has not handled.
func TestBackpressure(t *testing.T) {
	received := make(chan message, 2*maxUnacked)
	connectDart(t, func(msg message) { received <- msg })
	Export("endless", func(ctx context.Context) <-chan int {
		ch := make(chan int)
		go func() {
			defer close(ch)
			for i := 0; ; i++ {
				select {
				case ch <- i:
				case <-ctx.Done():
					return
				}
			}
		}()
		return ch
	})
	send(`{"type":"subscribe","id":1,"channel":"endless","data":[]}`)
	count := func() int {
		for n := 0; ; n++ {
			select {
			case <-received:
			case <-time.After(50 * time.Millisecond):
				return n
			}
		}
	}
	if n := count(); n != maxUnacked {
		t.Fatalf("got %d values without credit, want %d", n, maxUnacked)
	}
	send(fmt.Sprintf(`{"type":"credit","id":1,"data":[10,%d]}`, 10*30))
	if n := count(); n < 10 || n > 11 {
		t.Errorf("got %d values for a credit of 10", n)
	}
	send(`{"type":"cancel","id":1}`)
}

// On the web the first message of a run of Go is pushed, the next ones
// ring and wait in the outbox.
func TestPush(t *testing.T) {
	disconnect()
	Emit("early", 1)
	var pushed []message
	rings := 0
	decode := func(segments [][]byte) (msgs []message) {
		var raw []byte
		for _, s := range segments {
			raw = append(raw, s...)
		}
		for len(raw) > 0 {
			msg, rest, err := readFrame(raw)
			if err != nil {
				t.Fatal(err)
			}
			msgs = append(msgs, msg)
			raw = rest
		}
		return msgs
	}
	connectPush(func() { rings++ }, func(segments [][]byte, size int) {
		pushed = append(pushed, decode(segments)...)
	})
	t.Cleanup(func() { connect(func() {}) })
	if len(pushed) != 1 || pushed[0].Channel != "early" {
		t.Fatalf("queued message not pushed: %+v", pushed)
	}

	resumePush() // Dart sends
	Emit("first", []byte{1, 2})
	Emit("second", 2)
	Emit("third", 3)
	if len(pushed) != 2 || pushed[1].Channel != "first" || !bytes.Equal(pushed[1].blobs[0].data, []byte{1, 2}) {
		t.Errorf("pushed %+v", pushed)
	}
	if rings != 1 {
		t.Errorf("rang %d times, want once", rings)
	}
	resumePush()
	Emit("fourth", 4) // the outbox is not empty, it must not overtake
	segments, _ := take()
	var channels []string
	for _, msg := range decode(segments) {
		channels = append(channels, msg.Channel)
	}
	if fmt.Sprint(channels) != "[second third fourth]" {
		t.Errorf("took %v", channels)
	}
	Emit("fifth", 5)
	if len(pushed) != 3 || pushed[2].Channel != "fifth" {
		t.Errorf("not pushed after take: %+v", pushed)
	}
}
