//go:build !js

package fluffy

/*
#include <stdint.h>
#include <stdlib.h>

typedef void (*fluffy_ring)(void);

// Go cannot call a C function pointer directly.
static void fluffy_call(fluffy_ring ring) {
	ring();
}

// The free that matches C.malloc, for Dart's finalizers.
static void* fluffy_free(void) {
	return (void*)&free;
}
*/
import "C"
import "unsafe"

// The native transport. Dart passes a NativeCallable.listener to
// FluffyConnect, the doorbell: Go calls it from any thread when the outbox
// gets messages, and Dart takes all of them at once with FluffyTake, in a
// malloc'ed buffer it frees with a finalizer. Dart sends each message with
// FluffySend in a buffer from FluffyAlloc, which Go frees.

//export FluffyConnect
func FluffyConnect(ring C.fluffy_ring) {
	connect(func() { C.fluffy_call(ring) })
}

// FluffyAlloc returns a buffer of length bytes for FluffySend.
//
//export FluffyAlloc
func FluffyAlloc(length C.int64_t) unsafe.Pointer {
	return C.malloc(C.size_t(length))
}

// FluffySend hands one frame to Go, which frees data. It returns 1 when Dart
// has to call FluffyTake right away: Go waits briefly for the answer to a
// request or the first value of a subscription and returns it that way,
// without the doorbell.
//
//export FluffySend
func FluffySend(data unsafe.Pointer, length C.int64_t) C.int32_t {
	raw := unsafe.Slice((*byte)(data), int(length))
	if await(deliver(raw, func() { C.free(data) })) {
		return 1
	}
	return 0
}

// FluffyTake returns the outbox in one malloc'ed buffer and its length, or
// NULL when it is empty.
//
//export FluffyTake
func FluffyTake(length *C.int64_t) unsafe.Pointer {
	segments, size := take()
	*length = C.int64_t(size)
	if size == 0 {
		return nil
	}
	p := C.malloc(C.size_t(size))
	buf := unsafe.Slice((*byte)(p), size)
	n := 0
	for _, s := range segments {
		n += copy(buf[n:], s)
	}
	return p
}

// FluffyFreeFunction returns the address of free, the finalizer of the
// buffers from FluffyTake.
//
//export FluffyFreeFunction
func FluffyFreeFunction() unsafe.Pointer {
	return C.fluffy_free()
}

// Serve is only needed on the web. Native builds are libraries, their main
// function never runs.
func Serve() {}

// Yield lets the browser run on the web, where Go shares its main thread.
// Natively Go has threads of its own, it does nothing.
func Yield() {}
