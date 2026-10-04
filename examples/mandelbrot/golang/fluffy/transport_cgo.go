//go:build !js

package fluffy

/*
#include <stdint.h>
#include <stdlib.h>

typedef void (*fluffy_callback)(uint8_t* data, int64_t length);

// Go cannot call a C function pointer directly.
static void fluffy_call(fluffy_callback callback, uint8_t* data, int64_t length) {
	callback(data, length);
}
*/
import "C"
import "unsafe"

// The native transport. Dart sends with FluffySend and passes a
// NativeCallable.listener to FluffyConnect, which Go calls from any thread
// with a malloc'ed copy of each message. Dart copies it and releases it with
// FluffyFree.

//export FluffyConnect
func FluffyConnect(callback C.fluffy_callback) {
	connect(func(msg []byte) {
		C.fluffy_call(callback, (*C.uint8_t)(C.CBytes(msg)), C.int64_t(len(msg)))
	})
}

// FluffySend copies the message, Dart keeps ownership of data.
//
//export FluffySend
func FluffySend(data *C.uint8_t, length C.int64_t) {
	deliver(C.GoBytes(unsafe.Pointer(data), C.int(length)))
}

//export FluffyFree
func FluffyFree(data unsafe.Pointer) {
	C.free(data)
}

// Serve is only needed on the web. Native builds are libraries, their main
// function never runs.
func Serve() {}
