import 'dart:async';
import 'dart:isolate';

import 'package:flutter/foundation.dart';

import 'package:mandelbrot/golang/golang.dart' as golang;

// Benchmarks of the bridge itself: Go does nothing (golang/bench), so the
// time is what a call, a byte or a stream value costs between Dart and Go,
// including the JSON encoding (bytes travel as base64).

/// The result of a benchmark: [ops] operations moving [bytes] payload bytes
/// in [elapsed].
class Measurement {
  const Measurement(this.ops, this.bytes, this.elapsed);

  final int ops;
  final int bytes;
  final Duration elapsed;

  double get microsPerOp => elapsed.inMicroseconds / ops;
  double get opsPerSecond => ops / elapsed.inMicroseconds * 1e6;
  double get megabytesPerSecond => bytes / elapsed.inMicroseconds;

  @override
  String toString() => [
    '${microsPerOp.toStringAsFixed(microsPerOp < 100 ? 1 : 0)} µs/op',
    '${_count(opsPerSecond)} op/s',
    if (bytes > 0) '${megabytesPerSecond.toStringAsFixed(1)} MB/s',
  ].join(' · ');
}

String _count(double n) => n >= 1000
    ? '${(n / 1000).toStringAsFixed(n >= 1e4 ? 0 : 1)} k'
    : n.toStringAsFixed(0);

/// One-way delays in microseconds, measured with the wall clock both sides
/// share.
class Latency {
  Latency(List<int> micros) : micros = [...micros]..sort();

  final List<int> micros;

  int percentile(int p) => micros[(micros.length - 1) * p ~/ 100];

  @override
  String toString() => 'median ${percentile(50)} µs · p90 ${percentile(90)} µs';
}

class Benchmark {
  const Benchmark(this.name, this.description, this.size, this._run);

  final String name;
  final String description;

  /// The number of operations in a full run.
  final int size;

  /// Runs n operations and returns a [Measurement] or [Latency].
  final Future<Object> Function(int n) _run;

  /// Runs [size] operations after a warm-up, [quick] runs a tenth of them.
  Future<Object> run({bool quick = false}) async {
    await _run(1);
    return _run(quick ? (size ~/ 10).clamp(1, size) : size);
  }
}

const _kib = 1024, _mib = 1024 * 1024;

Future<Measurement> _time(
  int ops,
  int bytes,
  Future<void> Function() body,
) async {
  final watch = Stopwatch()..start();
  await body();
  return Measurement(ops, bytes, watch.elapsed);
}

Benchmark _send(String size, int bytes, int ops) {
  final payload = Uint8List(bytes);
  return Benchmark(
    'Dart → Go $size',
    'one at a time, Go returns the length',
    ops,
    (n) => _time(n, n * bytes, () async {
      for (var i = 0; i < n; i++) {
        await golang.bench.take(payload);
      }
    }),
  );
}

Benchmark _receive(String size, int bytes, int ops) => Benchmark(
  'Go → Dart $size',
  'one at a time',
  ops,
  (n) => _time(n, n * bytes, () async {
    for (var i = 0; i < n; i++) {
      await golang.bench.bytes(bytes);
    }
  }),
);

final benchmarks = [
  // On the web Go runs on the same thread as Dart, there is no hop to
  // measure.
  if (!kIsWeb)
    Benchmark(
      'Go → Dart one way',
      'from the Go handler to the awaiting Dart code',
      500,
      (n) async {
        final delays = <int>[];
        for (var i = 0; i < n; i++) {
          final go = await golang.bench.now();
          delays.add(DateTime.now().microsecondsSinceEpoch - go);
        }
        return Latency(delays);
      },
    ),
  if (!kIsWeb)
    Benchmark(
      'isolate → main one way',
      'Dart only, for comparison: a message from another isolate',
      500,
      _isolateLatency,
    ),
  Benchmark(
    'call',
    'one empty call at a time: the round trip latency',
    2000,
    (n) => _time(n, 0, () async {
      for (var i = 0; i < n; i++) {
        await golang.bench.ping();
      }
    }),
  ),
  Benchmark(
    'calls in flight',
    'empty calls all started at once: the throughput',
    10000,
    (n) => _time(n, 0, () async {
      await Future.wait([for (var i = 0; i < n; i++) golang.bench.ping()]);
    }),
  ),
  _send('1 KiB', _kib, 2000),
  _send('1 MiB', _mib, 40),
  _send('16 MiB', 16 * _mib, 4),
  _receive('1 KiB', _kib, 2000),
  _receive('1 MiB', _mib, 40),
  _receive('16 MiB', 16 * _mib, 4),
  Benchmark(
    'stream values',
    'ints from a Go channel',
    20000,
    (n) => _time(n, 0, () => golang.bench.values(n).drain<void>()),
  ),
  Benchmark(
    'stream 256 KiB chunks',
    'byte slices from a Go channel',
    80,
    (n) => _time(
      n,
      n * 256 * _kib,
      () => golang.bench.chunks(n, 256 * _kib).drain<void>(),
    ),
  ),
];

/// How long a message from another isolate takes to reach the main
/// isolate: the delay before Dart handles anything Go sends, which fluffy
/// cannot go below.
Future<Latency> _isolateLatency(int n) async {
  final port = ReceivePort();
  final isolate = await Isolate.spawn(_echoTime, port.sendPort);
  final replies = StreamIterator(port);
  try {
    await replies.moveNext();
    final other = replies.current as SendPort;
    final delays = <int>[];
    for (var i = 0; i < n; i++) {
      other.send(null);
      await replies.moveNext();
      delays.add(
        DateTime.now().microsecondsSinceEpoch - (replies.current as int),
      );
    }
    return Latency(delays);
  } finally {
    await replies.cancel();
    isolate.kill();
  }
}

/// Answers every message with the time it was answered.
void _echoTime(SendPort reply) {
  final port = ReceivePort();
  reply.send(port.sendPort);
  port.listen((_) => reply.send(DateTime.now().microsecondsSinceEpoch));
}
