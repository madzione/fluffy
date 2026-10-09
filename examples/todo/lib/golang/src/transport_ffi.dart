import 'dart:async';
import 'dart:ffi';
import 'dart:io';
import 'dart:typed_data';

import 'frame.dart';

DynamicLibrary _open() {
  // iOS links the Go archive statically into the app.
  if (Platform.isIOS) return DynamicLibrary.process();

  final exeDir = File(Platform.resolvedExecutable).parent.path;
  if (Platform.isMacOS) {
    return DynamicLibrary.open('$exeDir/../Frameworks/libgolang.dylib');
  }
  if (Platform.isLinux) return DynamicLibrary.open('$exeDir/lib/libgolang.so');
  if (Platform.isWindows) return DynamicLibrary.open('golang.dll');
  return DynamicLibrary.open('libgolang.so');
}

typedef _Ring = Void Function();

/// Connects to the Go library over dart:ffi (golang/fluffy/transport_cgo.go).
Send connectGo(void Function(Uint8List frames) receive) {
  final lib = _open();
  final connect = lib
      .lookupFunction<
        Void Function(Pointer<NativeFunction<_Ring>>),
        void Function(Pointer<NativeFunction<_Ring>>)
      >('FluffyConnect');
  final alloc = lib
      .lookupFunction<
        Pointer<Uint8> Function(Int64),
        Pointer<Uint8> Function(int)
      >('FluffyAlloc');
  final send = lib
      .lookupFunction<
        Int32 Function(Pointer<Uint8>, Int64),
        int Function(Pointer<Uint8>, int)
      >('FluffySend');
  final take = lib
      .lookupFunction<
        Pointer<Uint8> Function(Pointer<Int64>),
        Pointer<Uint8> Function(Pointer<Int64>)
      >('FluffyTake');
  final free = lib
      .lookupFunction<
        Pointer<NativeFinalizerFunction> Function(),
        Pointer<NativeFinalizerFunction> Function()
      >('FluffyFreeFunction')();

  final length = alloc(8).cast<Int64>(); // lives as long as the app
  var pulling = false;

  // Takes every message Go has for Dart, in one buffer that is freed once
  // nothing refers to it: byte data in the messages can be views of it.
  void pull() {
    pulling = false;
    final frames = take(length);
    if (frames == nullptr) return;
    receive(
      frames.asTypedList(length.value, finalizer: free, token: frames.cast()),
    );
  }

  // The doorbell: Go calls it from its own threads when it has messages,
  // the listener runs pull on this isolate later.
  connect(NativeCallable<_Ring>.listener(pull).nativeFunction);

  return (json, blobs) {
    final size = frameSize(json, blobs);
    final buffer = alloc(size);
    writeFrame(buffer.asTypedList(size), json, blobs);
    // Go frees the buffer. For a request it waits briefly for the answer
    // and returns 1 when it has one, which is then taken right away instead
    // of on the next turn of the event loop.
    if (send(buffer, size) != 0 && !pulling) {
      pulling = true;
      scheduleMicrotask(pull);
    }
  };
}
