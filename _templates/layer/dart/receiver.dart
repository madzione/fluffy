part of 'bridge.dart';

/// Handles messages from Go.
typedef Handler = FutureOr<Object?> Function(Object? data);

/// Produces the values of a stream for a subscription.
typedef StreamHandler = Stream<Object?> Function(Object? data);

/// Receives messages from Go. The data is decoded JSON, except a Go []byte
/// sent as the data itself or as a field of a struct sent as the data,
/// which arrives as a Uint8List instead of a base64 String.
class Receiver {
  Receiver._();

  final _streams = <String, StreamController<Object?>>{};
  final _handlers = <String, Handler>{};
  final _streamHandlers = <String, StreamHandler>{};

  /// The data Go emits on [channel] (fluffy.Emit). Messages without a
  /// listener are dropped.
  Stream<T> on<T>(String channel) => _streams
      .putIfAbsent(channel, StreamController<Object?>.broadcast)
      .stream
      .cast<T>();

  /// Answers Go requests on [channel] (fluffy.Request) with the result of
  /// [handler]. A thrown error fails the request in Go. Registering a
  /// channel again replaces its handler.
  void handle(String channel, Handler handler) => _handlers[channel] = handler;

  /// Serves subscriptions to [channel] ([Emitter.subscribe]) with the
  /// stream [handler] returns. Go does not subscribe to Dart, this is for
  /// [Bridge.fake], where it plays a Go function that returns a channel.
  void stream(String channel, StreamHandler handler) =>
      _streamHandlers[channel] = handler;

  /// Unregisters the handlers of [channel].
  void remove(String channel) {
    _handlers.remove(channel);
    _streamHandlers.remove(channel);
  }
}
