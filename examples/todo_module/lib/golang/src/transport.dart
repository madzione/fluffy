// connectGo connects to the Go library and returns the function that sends
// to it: over dart:ffi on native platforms, over the JavaScript globals of
// the WebAssembly module on the web.
export 'transport_ffi.dart' if (dart.library.js_interop) 'transport_web.dart';

/// Sends one encoded message to the other side.
typedef Send = void Function(String message);
