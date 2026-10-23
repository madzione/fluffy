// The Go library is the code asset of the fluffy_golang package (golang/):
// its build hook builds it and Flutter bundles it with the app. Dart
// resolves the symbols on the first call, so tests that only use
// Bridge.fake() never load it.
@DefaultAsset('package:fluffy_golang/golang')
library;

import 'dart:async';
import 'dart:ffi';
import 'dart:typed_data';

import 'frame.dart';

typedef _Ring = Void Function();

// The functions golang/fluffy/transport_cgo.go exports.
@Native<Void Function(Pointer<NativeFunction<_Ring>>)>(symbol: 'FluffyConnect')
external void _connect(Pointer<NativeFunction<_Ring>> ring);

@Native<Pointer<Uint8> Function(Int64)>(symbol: 'FluffyAlloc')
external Pointer<Uint8> _alloc(int size);

@Native<Int32 Function(Pointer<Uint8>, Int64)>(symbol: 'FluffySend')
external int _send(Pointer<Uint8> frame, int length);

@Native<Pointer<Uint8> Function(Pointer<Int64>)>(symbol: 'FluffyTake')
external Pointer<Uint8> _take(Pointer<Int64> length);

@Native<Pointer<NativeFinalizerFunction> Function()>(
  symbol: 'FluffyFreeFunction',
)
external Pointer<NativeFinalizerFunction> _freeFunction();

/// Connects to the Go library over dart:ffi (golang/fluffy/transport_cgo.go).
Send connectGo(void Function(Uint8List frames) receive) {
  final free = _freeFunction();
  final length = _alloc(8).cast<Int64>(); // lives as long as the app
  var pulling = false;

  // Takes every message Go has for Dart, in one buffer that is freed once
  // nothing refers to it: byte data in the messages can be views of it.
  void pull() {
    pulling = false;
    final frames = _take(length);
    if (frames == nullptr) return;
    receive(
      frames.asTypedList(length.value, finalizer: free, token: frames.cast()),
    );
  }

  // The doorbell: Go calls it from its own threads when it has messages,
  // the listener runs pull on this isolate later.
  _connect(NativeCallable<_Ring>.listener(pull).nativeFunction);

  return (json, blobs) {
    final size = frameSize(json, blobs);
    final buffer = _alloc(size);
    writeFrame(buffer.asTypedList(size), json, blobs);
    // Go frees the buffer. For a request it waits briefly for the answer
    // and returns 1 when it has one, which is then taken right away instead
    // of on the next turn of the event loop.
    if (_send(buffer, size) != 0 && !pulling) {
      pulling = true;
      scheduleMicrotask(pull);
    }
  };
}
