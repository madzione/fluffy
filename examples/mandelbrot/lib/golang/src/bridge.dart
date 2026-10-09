// Managed by fluffy: fluffy init and fluffy update replace lib/golang/src/,
// so changes made here are lost.

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

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
/// the same `id`, stopped early by `cancel`, and paced by `credit`: the
/// subscriber acknowledges the values it has handled). Byte data
/// (Uint8List) travels next to the JSON instead of in it, `bin` lists where
/// it belongs in `data`.
class Bridge {
  Bridge._(Send Function(void Function(Uint8List frames) receive) connect) {
    _send = connect(_receiveFrames);
  }

  static Bridge? _instance;

  static Bridge get instance => _instance ??= Bridge._(connectGo);

  /// Replaces Go with an in-memory bridge for tests and returns it. Register
  /// handlers on its receiver to play the Go side.
  @visibleForTesting
  static Bridge fake() {
    late final Bridge app, go;
    // Deliver asynchronously, like the real transports.
    Send sendTo(Bridge Function() peer) => (json, blobs) {
      final frame = Uint8List(frameSize(json, blobs));
      writeFrame(frame, json, blobs);
      scheduleMicrotask(() => peer()._receiveFrames(frame));
    };
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
  final _subscriptions = <int, _Subscription>{};
  final _serving = <int, _Serving>{};
  final _credits = <int>{};

  static final _decoder = utf8.decoder.fuse(json.decoder);

  /// Sends [message] and returns the size of its frame.
  int _post(Map<String, Object?> message) {
    final blobs = <Uint8List>[];
    if (message['data'] case final data?) {
      final paths = <List<Object>>[];
      message['data'] = _extract(data, paths, blobs);
      if (paths.isNotEmpty) message['bin'] = paths;
    }
    final encoded = utf8.encode(jsonEncode(message));
    _send(encoded, blobs);
    return frameSize(encoded, blobs);
  }

  /// Takes the Uint8Lists out of [data] where Go puts them back without
  /// JSON: the data itself, an element of a list (an argument), and a value
  /// of a map (a struct field) in the data or in such a list. Containers on
  /// the way are copied, the caller's data does not change.
  static Object? _extract(
    Object? data,
    List<List<Object>> paths,
    List<Uint8List> blobs,
  ) {
    Object? blob(Uint8List bytes, List<Object> path) {
      paths.add(path);
      blobs.add(bytes);
      return null;
    }

    Object? fromMap(Object? map, List<Object> path) {
      if (map is! Map) return map;
      Map<Object?, Object?>? copy;
      for (final MapEntry(:key, :value) in map.entries) {
        if (value is Uint8List && key is String) {
          (copy ??= Map<Object?, Object?>.of(map))[key] = blob(value, [
            ...path,
            key,
          ]);
        }
      }
      return copy ?? map;
    }

    switch (data) {
      case Uint8List():
        return blob(data, const []);
      case Map():
        return fromMap(data, const []);
      case List() when data is! TypedData:
        List<Object?>? copy;
        for (var i = 0; i < data.length; i++) {
          final item = data[i];
          final sent = item is Uint8List ? blob(item, [i]) : fromMap(item, [i]);
          if (!identical(sent, item)) {
            (copy ??= List<Object?>.of(data))[i] = sent;
          }
        }
        return copy ?? data;
    }
    return data;
  }

  void _receiveFrames(Uint8List frames) {
    final view = ByteData.sublistView(frames);
    var offset = 0;
    while (offset < frames.length) {
      final start = offset;
      final length = view.getUint32(offset, Endian.little);
      offset += 8;
      final message =
          _decoder.convert(
                Uint8List.sublistView(frames, offset, offset + length),
              )
              as Map<String, dynamic>;
      offset += length;
      if (message['bin'] case final List<dynamic> paths) {
        var data = message['data'];
        for (final path in paths) {
          final length = view.getUint32(offset, Endian.little);
          offset += 4;
          data = _insert(
            data,
            path as List<dynamic>,
            _blob(frames, offset, length),
          );
          offset += length;
        }
        message['data'] = data;
      }
      try {
        _receive(message, offset - start);
      } catch (error, stack) {
        // The other messages of the batch are still handled.
        Zone.current.handleUncaughtError(error, stack);
      }
    }
  }

  /// Small blobs are copied, so they do not keep all frames of their batch
  /// in memory.
  static Uint8List _blob(Uint8List frames, int offset, int length) {
    final view = Uint8List.sublistView(frames, offset, offset + length);
    return length < 1 << 16 ? Uint8List.fromList(view) : view;
  }

  static Object? _insert(Object? data, List<dynamic> path, Uint8List blob) {
    if (path.isEmpty) return blob;
    var node = data;
    for (final step in path.take(path.length - 1)) {
      node = step is int ? (node as List)[step] : (node as Map)[step];
    }
    switch (path.last) {
      case final int i:
        (node as List)[i] = blob;
      case final key:
        (node as Map)[key] = blob;
    }
    return data;
  }

  /// Handles one message, [size] is the size of its frame.
  void _receive(Map<String, dynamic> message, int size) {
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
        } else if (_subscriptions.remove(id) case final subscription?) {
          subscription.controller
            ..addError(
              GolangException(
                subscription.channel,
                message['error'] as String,
              ),
            )
            ..close();
        }
      case 'subscribe':
        _serve(id!, message['channel'] as String, data);
      case 'next':
        if (_subscriptions[id] case final subscription?) {
          subscription.controller.add(data);
          subscription
            ..count += 1
            ..bytes += size;
          _credit(id!);
        }
      case 'done':
        _subscriptions.remove(id)?.controller.close();
      case 'cancel':
        _serving.remove(id)?.subscription.cancel();
      case 'credit':
        _serving[id]?.credited(data as List<dynamic>);
      default:
        debugPrint('fluffy: unknown message type ${message['type']}');
    }
  }

  /// Acknowledges the values of subscription [id] once the messages at hand
  /// are handled, unless its listener is paused: the other side stops
  /// sending while too many are not acknowledged.
  ///
  /// On the web Go runs on this thread and sends the next values as soon as
  /// it gets the credit, so the credit waits for a timer: a fast stream
  /// would otherwise keep the browser in microtasks, without timers or
  /// frames.
  void _credit(int id) {
    if (_credits.isEmpty) {
      kIsWeb ? Timer.run(_sendCredits) : scheduleMicrotask(_sendCredits);
    }
    _credits.add(id);
  }

  void _sendCredits() {
    for (final id in _credits) {
      final subscription = _subscriptions[id];
      if (subscription == null ||
          subscription.controller.isPaused ||
          subscription.count == 0) {
        continue;
      }
      _post({
        'type': 'credit',
        'id': id,
        'data': [subscription.count, subscription.bytes],
      });
      subscription.count = subscription.bytes = 0;
    }
    _credits.clear();
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
    final Stream<Object?> stream;
    try {
      final handler = receiver._streamHandlers[channel];
      if (handler == null) {
        throw StateError('no Dart stream for channel "$channel"');
      }
      stream = handler(data);
    } catch (error) {
      _post({'type': 'error', 'id': id, 'error': '$error'});
      return;
    }
    final serving = _Serving();
    _serving[id] = serving;
    serving.subscription = stream.listen(
      (value) => serving.sent(_post({'type': 'next', 'id': id, 'data': value})),
      onError: (Object error) {
        _serving.remove(id)?.subscription.cancel();
        _post({'type': 'error', 'id': id, 'error': '$error'});
      },
      onDone: () {
        _serving.remove(id);
        _post({'type': 'done', 'id': id});
      },
    );
  }
}

/// A stream from the other side this side listens to.
class _Subscription {
  _Subscription(this.channel, this.controller);

  final String channel;
  final StreamController<Object?> controller;

  /// The values received and the bytes of their frames, not acknowledged
  /// yet.
  int count = 0, bytes = 0;
}

// The values of a stream the other side has not acknowledged yet, sending
// waits before more. The same as in golang/fluffy/receiver.go.
const _maxUnacked = 1024, _maxUnackedBytes = 8 << 20;

/// A stream this side sends to the other side ([Bridge.fake] only, Go does
/// not subscribe to Dart).
class _Serving {
  late final StreamSubscription<Object?> subscription;
  int _count = 0, _bytes = 0;
  bool _held = false;

  bool get _behind => _count >= _maxUnacked || _bytes >= _maxUnackedBytes;

  void sent(int size) {
    _count += 1;
    _bytes += size;
    if (!_held && _behind) {
      _held = true;
      subscription.pause();
    }
  }

  void credited(List<dynamic> data) {
    _count -= data[0] as int;
    _bytes -= data[1] as int;
    if (_held && !_behind) {
      _held = false;
      subscription.resume();
    }
  }
}
