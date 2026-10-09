// connectGo connects to the Go library and returns the function that sends
// to it: over dart:ffi on native platforms, over the JavaScript globals of
// the WebAssembly module on the web. It passes the messages from Go to
// receive in batches of frames.
export 'frame.dart';
export 'transport_ffi.dart' if (dart.library.js_interop) 'transport_web.dart';
