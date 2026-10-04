/// Go in this app, over the fluffy message layer.
///
/// The typed functions in api.g.dart are generated from golang/ by
/// fluffy generate, api.dart exports them next to your own additions.
///
/// Underneath, Dart and Go exchange JSON messages on named channels. Send
/// with the [emitter] ([Emitter.emit], [Emitter.request],
/// [Emitter.subscribe]), react to Go with the [receiver] ([Receiver.on],
/// [Receiver.handle]). The Go side is the fluffy package in golang/fluffy/
/// with the same API.
library;

export 'api.dart';
export 'src/bridge.dart';
