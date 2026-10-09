part of 'bridge.dart';

/// Sends messages to Go. Data is encoded as JSON, so it can be null, bool,
/// num, String, or Lists and Maps with String keys of those. A Uint8List as
/// the data, as an element of a List data or as a value of a Map there is
/// sent as raw bytes, for a Go []byte.
class Emitter {
  Emitter._(this._bridge);

  final Bridge _bridge;

  /// Sends [data] to the Go handler of [channel] (fluffy.On or fluffy.Handle)
  /// without waiting for it.
  void emit(String channel, [Object? data]) {
    _bridge._post({'type': 'emit', 'channel': channel, 'data': data});
  }

  /// Sends [data] to the Go handler of [channel] (fluffy.Handle) and
  /// completes with its answer, or with a [GolangException] when it fails.
  ///
  /// [T] is the decoded JSON: int, double, String, bool,
  /// `Map<String, dynamic>` or `List<dynamic>`.
  Future<T> request<T>(String channel, [Object? data]) async {
    final id = ++_bridge._lastId;
    final completer = Completer<Object?>();
    _bridge._pending[id] = (channel, completer);
    _bridge._post({
      'type': 'request',
      'id': id,
      'channel': channel,
      'data': data,
    });
    return await completer.future as T;
  }

  /// Listens to the Go stream on [channel], called with [data]: a Go
  /// function that returns a channel (`<-chan T`). Go starts it when the
  /// stream is listened to and cancels its context when the subscription is
  /// cancelled. The stream ends when Go closes the channel, and fails with a
  /// [GolangException] when Go returns an error.
  ///
  /// Go sends ahead of the listener, but only so far: while the
  /// subscription is paused, Go stops after 1024 values or 8 MiB.
  Stream<Object?> subscribe(String channel, [Object? data]) {
    late final StreamController<Object?> controller;
    int? id;
    controller = StreamController<Object?>(
      onListen: () {
        id = ++_bridge._lastId;
        _bridge._subscriptions[id!] = _Subscription(channel, controller);
        _bridge._post({
          'type': 'subscribe',
          'id': id,
          'channel': channel,
          'data': data,
        });
      },
      onResume: () => _bridge._credit(id!),
      onCancel: () {
        if (_bridge._subscriptions.remove(id) != null) {
          _bridge._post({'type': 'cancel', 'id': id});
        }
      },
    );
    return controller.stream;
  }
}
