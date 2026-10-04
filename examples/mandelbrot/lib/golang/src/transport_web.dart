import 'dart:async';
import 'dart:js_interop';

@JS('golang.connect')
external void _connect(JSFunction receive);

@JS('golang.send')
external void _send(String message);

/// Connects to the Go WebAssembly module, which registers globalThis.golang
/// (golang/fluffy/transport_js.go) before Flutter starts.
void Function(String message) connectGo(void Function(String message) receive) {
  // Go calls back synchronously, handle the message once Go has returned.
  _connect(
    ((String message) => scheduleMicrotask(() => receive(message))).toJS,
  );
  return (message) => _send(message);
}
