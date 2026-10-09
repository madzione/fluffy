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
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
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
	typeCredit    = "credit"    // Dart handled stream values, data is [count, bytes]

	// Never sent: fails a Request to a Dart isolate that is gone.
	typeReconnect = "reconnect"
)

// message is one frame on the wire, little endian:
//
//	uint32 length of the JSON envelope
//	uint32 number of blobs
//	the JSON envelope {type, id, channel, data, error, bin}
//	per blob: uint32 length, the bytes
//
// Byte slices travel as blobs instead of base64 in data where the receiver
// can put them back cheaply (see encodeData). bin holds the path of each
// blob in data, in the order of the blobs.
type message struct {
	Type    string          `json:"type"`
	ID      int64           `json:"id,omitempty"`
	Channel string          `json:"channel,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
	Bin     [][]any         `json:"bin,omitempty"`
	blobs   []blob
}

// blob is a byte slice of a message's data that is sent as is.
type blob struct {
	path []any // keys (string) and indexes (int, float64 from JSON) into data
	data []byte
}

// frame encodes msg into segments to be sent one after the other, and
// returns their total size. The blobs are copied, so a value that changes
// after it was sent does not change the message.
func (msg *message) frame() ([][]byte, int) {
	head := make([]byte, 8, 128+len(msg.Data))
	head = append(head, `{"type":"`...)
	head = append(head, msg.Type...)
	head = append(head, '"')
	if msg.ID != 0 {
		head = append(head, `,"id":`...)
		head = strconv.AppendInt(head, msg.ID, 10)
	}
	if msg.Channel != "" {
		head = append(head, `,"channel":`...)
		head = appendJSON(head, msg.Channel)
	}
	if len(msg.Data) > 0 {
		head = append(head, `,"data":`...)
		head = append(head, msg.Data...)
	}
	if msg.Error != "" {
		head = append(head, `,"error":`...)
		head = appendJSON(head, msg.Error)
	}
	if len(msg.blobs) > 0 {
		head = append(head, `,"bin":[`...)
		for i, b := range msg.blobs {
			if i > 0 {
				head = append(head, ',')
			}
			if b.path == nil {
				head = append(head, "[]"...)
			} else {
				head = appendJSON(head, b.path)
			}
		}
		head = append(head, ']')
	}
	head = append(head, '}')
	binary.LittleEndian.PutUint32(head, uint32(len(head)-8))
	binary.LittleEndian.PutUint32(head[4:], uint32(len(msg.blobs)))

	segments := [][]byte{head}
	size := len(head)
	for _, b := range msg.blobs {
		segment := make([]byte, 4+len(b.data))
		binary.LittleEndian.PutUint32(segment, uint32(len(b.data)))
		copy(segment[4:], b.data)
		segments = append(segments, segment)
		size += len(segment)
	}
	return segments, size
}

// appendJSON appends v, a string or a path, as JSON.
func appendJSON(b []byte, v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err) // strings and paths always encode
	}
	return append(b, raw...)
}

// readFrame decodes the first frame of raw and returns the rest. The
// message does not refer to raw.
func readFrame(raw []byte) (msg message, rest []byte, err error) {
	if len(raw) < 8 {
		return msg, nil, errors.New("truncated frame")
	}
	size := int(binary.LittleEndian.Uint32(raw))
	count := int(binary.LittleEndian.Uint32(raw[4:]))
	raw = raw[8:]
	if len(raw) < size {
		return msg, nil, errors.New("truncated frame")
	}
	if err := json.Unmarshal(raw[:size], &msg); err != nil {
		return msg, nil, err
	}
	raw = raw[size:]
	if count != len(msg.Bin) {
		return msg, nil, fmt.Errorf("%d blobs for %d paths", count, len(msg.Bin))
	}
	for _, path := range msg.Bin {
		if len(raw) < 4 {
			return msg, nil, errors.New("truncated frame")
		}
		n := int(binary.LittleEndian.Uint32(raw))
		raw = raw[4:]
		if len(raw) < n {
			return msg, nil, errors.New("truncated frame")
		}
		data := make([]byte, n)
		copy(data, raw)
		msg.blobs = append(msg.blobs, blob{path: path, data: data})
		raw = raw[n:]
	}
	msg.Bin = nil
	return msg, raw, nil
}

// maxQueued limits the messages kept while Dart is not connected yet.
const maxQueued = 1024

// out is the outbox. Go never calls into Dart with a message: it collects
// the frames and rings Dart's doorbell once, then Dart takes all of them in
// one call. While Dart is busy, messages pile up and are handled together.
var out struct {
	sync.Mutex
	ring     func()              // tells Dart to take the outbox, nil until connected
	push     func([][]byte, int) // hands Dart a message directly (web)
	conn     atomic.Int64        // counts the connections, changed with the lock held
	segments [][]byte
	size     int
	queued   int  // messages in the outbox while not connected
	rung     bool // Dart is going to take the outbox: rung, or told by a send
	waiting  bool // Dart is blocked in a send (await), it takes afterwards
	pushed   bool // web: a message was pushed since Dart last took or sent
}

// errReconnected fails what belongs to a Dart isolate that a hot restart
// replaced.
var errReconnected = errors.New("Dart connected again (hot restart)")

// connect sets the doorbell of Dart and rings it for everything emitted
// before Dart was listening. Dart connects again after a hot restart and
// numbers its requests and streams from 1 again, so everything of the
// previous Dart isolate ends: its streams are cancelled, late answers to its
// requests are dropped instead of reaching the new isolate, and Go's
// requests to it fail.
func connect(ring func()) {
	connectWith(ring, nil)
}

// connectPush connects a Dart that runs on Go's thread (the web), where a
// call from JavaScript into Go costs much more than the other way round.
// While nothing waits for Dart, Go pushes a message right away, Dart
// handles it once Go has returned. The messages after it in the same run of
// Go go to the outbox and ring, Dart takes them together with one call.
// Go only pushes while the outbox is empty, so the order holds.
func connectPush(ring func(), push func(segments [][]byte, size int)) {
	connectWith(ring, push)
}

// resumePush is called when Dart calls into Go: Go is in a new run, the
// next message can be pushed again.
func resumePush() {
	out.Lock()
	out.pushed = false
	out.Unlock()
}

func connectWith(ring func(), push func([][]byte, int)) {
	begin()
	cancelSubscriptions()
	out.Lock()
	defer out.Unlock()
	reconnect := out.ring != nil
	conn := out.conn.Add(1)
	out.ring, out.push = ring, push
	out.queued = 0
	out.rung, out.waiting, out.pushed = false, false, false
	if reconnect {
		out.segments, out.size = nil, 0
		failPending(conn)
	}
	switch {
	case out.size == 0:
	case push != nil:
		out.pushed = true
		push(out.segments, out.size)
		out.segments, out.size = nil, 0
	default:
		out.rung = true
		ring()
	}
}

// post sends msg to Dart, or queues it until Dart connects, and returns the
// size of its frame. conn is the connection msg belongs to, 0 for any. A
// message for an earlier connection is dropped with errReconnected.
func post(conn int64, msg message) (int, error) {
	segments, size := msg.frame()
	out.Lock()
	defer out.Unlock()
	switch {
	case conn != 0 && conn != out.conn.Load():
		return 0, errReconnected
	case out.ring == nil && out.queued >= maxQueued:
		log.Printf("fluffy: Dart is not connected, dropped message on %q", msg.Channel)
		return 0, fmt.Errorf("Dart is not connected and %d messages are queued", maxQueued)
	case out.ring == nil:
		out.queued++
	case out.push != nil && !out.pushed && out.size == 0:
		out.pushed = true
		out.push(segments, size)
		return size, nil
	}
	out.segments = append(out.segments, segments...)
	out.size += size
	// The ring is called with the lock held, so it never reaches a Dart
	// isolate that a reconnect has replaced.
	if out.ring != nil && !out.rung && !out.waiting {
		out.rung = true
		out.ring()
	}
	return size, nil
}

// take empties the outbox and returns its frames for Dart.
func take() ([][]byte, int) {
	out.Lock()
	defer out.Unlock()
	segments, size := out.segments, out.size
	out.segments, out.size, out.rung, out.pushed = nil, 0, false, false
	return segments, size
}

// syncWait is how long a native send waits for the answer to a request or
// the first message of a subscription. A message Go rings for reaches Dart
// with the next turn of its event loop, about a millisecond on desktop
// Linux, an answer ready within syncWait is handled right away instead.
const syncWait = 250 * time.Microsecond

// slow holds the channels that missed syncWait, sends to them do not wait
// until one of them is answered in time again.
var slow sync.Map // channel -> struct{}

// answer is the first answer to a request or subscription, which a native
// send may wait for.
type answer struct {
	channel string
	start   time.Time
	once    sync.Once
	done    chan struct{}
}

func newAnswer(channel string) *answer {
	return &answer{channel: channel, start: time.Now(), done: make(chan struct{})}
}

// posted is called once the first answer is in the outbox.
func (a *answer) posted() {
	if a == nil {
		return
	}
	a.once.Do(func() {
		close(a.done)
		if time.Since(a.start) < syncWait {
			if _, ok := slow.Load(a.channel); ok {
				slow.Delete(a.channel)
			}
		}
	})
}

// await blocks the sending Dart thread until a is answered, for at most
// syncWait, and reports whether Dart has to take the outbox right away.
// Meanwhile messages do not ring Dart, it takes them with the answer.
func await(a *answer) bool {
	if a == nil {
		return false
	}
	out.Lock()
	out.waiting = true
	out.Unlock()

	timer := time.NewTimer(syncWait)
	select {
	case <-a.done:
	case <-timer.C:
		slow.Store(a.channel, struct{}{})
	}
	timer.Stop()

	out.Lock()
	defer out.Unlock()
	out.waiting = false
	if out.size > 0 && out.ring != nil {
		out.rung = true
		return true
	}
	return false
}

// in holds the frames from Dart that wait for the reader goroutine. Dart
// sends from one thread, frames are handled in that order.
var in struct {
	sync.Mutex
	queue   []incoming
	reading bool
}

type incoming struct {
	raw     []byte
	release func()
	conn    int64
}

// Frames with up to inlineJSON bytes of JSON and inlineSize bytes in all
// are decoded on the sending Dart thread, larger ones by the reader
// goroutine so Dart is not blocked. Blobs only need to be copied.
const (
	inlineJSON = 64 << 10
	inlineSize = 4 << 20
)

// deliver handles one frame from Dart and calls release once it is decoded
// (release may be nil). It returns the answer a send may wait for, if any.
// Answers resolve the waiting Request right away, handlers run in their own
// goroutine so a slow handler neither blocks Dart nor other messages.
func deliver(raw []byte, release func()) *answer {
	conn := out.conn.Load()
	in.Lock()
	if !in.reading && len(raw) <= inlineSize && len(raw) >= 4 && binary.LittleEndian.Uint32(raw) <= inlineJSON {
		in.Unlock()
		return handle(raw, release, conn, true)
	}
	in.queue = append(in.queue, incoming{raw, release, conn})
	if !in.reading {
		in.reading = true
		go read()
	}
	in.Unlock()
	return nil
}

// read handles the queued frames until the queue is empty.
func read() {
	for {
		in.Lock()
		if len(in.queue) == 0 {
			in.reading = false
			in.Unlock()
			return
		}
		next := in.queue[0]
		in.queue[0] = incoming{}
		in.queue = in.queue[1:]
		in.Unlock()
		handle(next.raw, next.release, next.conn, false)
	}
}

// handle decodes and dispatches a frame that arrived on the connection
// conn. Frames of a Dart isolate that is gone are dropped.
func handle(raw []byte, release func(), conn int64, inline bool) *answer {
	msg, _, err := readFrame(raw)
	if release != nil {
		release()
	}
	if err != nil {
		log.Printf("fluffy: invalid message from Dart: %v", err)
		return nil
	}
	if conn != out.conn.Load() {
		return nil
	}
	begin()
	var a *answer
	switch msg.Type {
	case typeResponse, typeError:
		resolve(msg)
	case typeEmit:
		go dispatch(conn, msg, nil)
	case typeRequest:
		a = newAnswer(msg.Channel)
		go dispatch(conn, msg, a)
	case typeSubscribe:
		a = newAnswer(msg.Channel)
		subscribe(conn, msg, a)
	case typeCancel:
		unsubscribe(msg.ID)
	case typeCredit:
		credit(msg)
	default:
		log.Printf("fluffy: unknown message type %q", msg.Type)
	}
	if a == nil || !inline {
		return nil
	}
	if _, ok := slow.Load(a.channel); ok {
		return nil
	}
	return a
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
