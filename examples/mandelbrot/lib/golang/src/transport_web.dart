import 'dart:async';
import 'dart:js_interop';
import 'dart:typed_data';

import 'frame.dart';

@JS('golang.connect')
external void _connect(JSFunction receive, JSFunction ring);

@JS('golang.send')
external void _send(JSUint8Array frame);

@JS('golang.take')
external JSUint8Array? _take();

/// Connects to the Go WebAssembly module, which registers globalThis.golang
/// (golang/fluffy/transport_js.go) before Flutter starts.
Send connectGo(void Function(Uint8List frames) receive) {
  // Go runs on this thread and calls back from inside its own calls: with a
  // message, or with the doorbell when more wait in its outbox. They are
  // handled together once Go has returned. Pushed messages come before the
  // ones in the outbox.
  final pushed = <Uint8List>[];
  var scheduled = false, more = false;
  void handle() {
    scheduled = false;
    final frames = [...pushed];
    pushed.clear();
    if (more) {
      more = false;
      if (_take() case final taken?) frames.add(taken.toDart);
    }
    frames.forEach(receive);
  }

  void schedule() {
    if (scheduled) return;
    scheduled = true;
    scheduleMicrotask(handle);
  }

  _connect(
    ((JSUint8Array frame) {
      pushed.add(frame.toDart);
      schedule();
    }).toJS,
    (() {
      more = true;
      schedule();
    }).toJS,
  );
  return (json, blobs) {
    final frame = Uint8List(frameSize(json, blobs));
    writeFrame(frame, json, blobs);
    _send(frame.toJS);
  };
}
