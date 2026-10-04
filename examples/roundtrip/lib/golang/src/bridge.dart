// Managed by fluffy: fluffy init and fluffy update replace lib/golang/src/,
// so changes made here are lost.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';

import 'transport.dart';

part 'emitter.dart';
part 'receiver.dart';

/// Sends messages to Go.
Emitter get emitter => Bridge.instance.emitter;

/// Handles messages from Go.
Receiver get receiver => Bridge.instance.receiver;

/// A Go handler failed, or there is no Go handler for the channel.
class GolangException implements Exception {
  GolangException(this.channel, this.message);

  final String channel;
  final String message;

  @override
  String toString() => 'GolangException($channel): $message';
}

/// One end of the message layer. The app has one, connected to the Go
/// library on first use.
///
/// Every message is a JSON object with a `type`: `emit` (no answer),
/// `request` (answered by a `response` or `error` with the same `id`), or
/// `subscribe` (answered by `next` values and a final `done` or `error` with
/// the same `id`, stopped early by `cancel`).
class Bridge {
  Bridge._(Send Function(void Function(String message) receive) connect) {
    _send = connect(_receive);
  }

  static Bridge? _instance;

  static Bridge get instance => _instance ??= Bridge._(connectGo);

  /// Replaces Go with an in-memory bridge for tests and returns it. Register
  /// handlers on its receiver to play the Go side.
  @visibleForTesting
  static Bridge fake() {
    late final Bridge app, go;
    // Deliver asynchronously, like the real transports.
    Send sendTo(Bridge Function() peer) =>
        (message) => scheduleMicrotask(() => peer()._receive(message));
    app = Bridge._((_) => sendTo(() => go));
    go = Bridge._((_) => sendTo(() => app));
    _instance = app;
    return go;
  }

  late final Send _send;
  late final Emitter emitter = Emitter._(this);
  late final Receiver receiver = Receiver._();

  int _lastId = 0;
  final _pending = <int, (String, Completer<Object?>)>{};
  final _subscriptions = <int, (String, StreamController<Object?>)>{};
  final _serving = <int, StreamSubscription<Object?>>{};

  void _post(Map<String, Object?> message) => _send(jsonEncode(message));

  void _receive(String raw) {
    final message = jsonDecode(raw) as Map<String, dynamic>;
    final id = message['id'] as int?;
    final data = message['data'];
    switch (message['type']) {
      case 'emit':
        receiver._streams[message['channel']]?.add(data);
      case 'request':
        _answer(id!, message['channel'] as String, data);
      case 'response':
        _pending.remove(id)?.$2.complete(data);
      case 'error':
        if (_pending.remove(id) case (final channel, final completer)) {
          completer.completeError(
            GolangException(channel, message['error'] as String),
          );
        } else if (_subscriptions.remove(id) case (
          final channel,
          final controller,
        )) {
          controller
            ..addError(GolangException(channel, message['error'] as String))
            ..close();
        }
      case 'subscribe':
        _serve(id!, message['channel'] as String, data);
      case 'next':
        _subscriptions[id]?.$2.add(data);
      case 'done':
        _subscriptions.remove(id)?.$2.close();
      case 'cancel':
        _serving.remove(id)?.cancel();
      default:
        debugPrint('fluffy: unknown message type ${message['type']}');
    }
  }

  Future<void> _answer(int id, String channel, Object? data) async {
    try {
      final handler = receiver._handlers[channel];
      if (handler == null) {
        throw StateError('no Dart handler for channel "$channel"');
      }
      _post({'type': 'response', 'id': id, 'data': await handler(data)});
    } catch (error) {
      _post({'type': 'error', 'id': id, 'error': '$error'});
    }
  }

  void _serve(int id, String channel, Object? data) {
    final handler = receiver._streamHandlers[channel];
    if (handler == null) {
      _post({
        'type': 'error',
        'id': id,
        'error': 'no Dart stream for channel "$channel"',
      });
      return;
    }
    _serving[id] = handler(data).listen(
      (value) => _post({'type': 'next', 'id': id, 'data': value}),
      onError: (Object error) {
        _serving.remove(id)?.cancel();
        _post({'type': 'error', 'id': id, 'error': '$error'});
      },
      onDone: () {
        _serving.remove(id);
        _post({'type': 'done', 'id': id});
      },
    );
  }
}
