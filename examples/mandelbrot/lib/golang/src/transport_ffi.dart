import 'dart:convert';
import 'dart:ffi';
import 'dart:io';

import 'package:ffi/ffi.dart';

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

typedef _Receive = Void Function(Pointer<Uint8> data, Int64 length);

/// Connects to the Go library over dart:ffi (golang/fluffy/transport_cgo.go).
void Function(String message) connectGo(void Function(String message) receive) {
  final lib = _open();
  final connect = lib
      .lookupFunction<
        Void Function(Pointer<NativeFunction<_Receive>>),
        void Function(Pointer<NativeFunction<_Receive>>)
      >('FluffyConnect');
  final send = lib
      .lookupFunction<
        Void Function(Pointer<Uint8>, Int64),
        void Function(Pointer<Uint8>, int)
      >('FluffySend');
  final free = lib
      .lookupFunction<
        Void Function(Pointer<Void>),
        void Function(Pointer<Void>)
      >('FluffyFree');

  // Go calls this from its own threads, the listener runs it on this isolate
  // later. The message is Go's malloc'ed copy, released once decoded.
  final callback = NativeCallable<_Receive>.listener((
    Pointer<Uint8> data,
    int length,
  ) {
    final message = utf8.decode(data.asTypedList(length));
    free(data.cast());
    receive(message);
  });
  connect(callback.nativeFunction);

  return (message) {
    final bytes = utf8.encode(message);
    final buffer = malloc<Uint8>(bytes.length);
    try {
      buffer.asTypedList(bytes.length).setAll(0, bytes);
      // Go copies the message before it returns.
      send(buffer, bytes.length);
    } finally {
      malloc.free(buffer);
    }
  };
}
