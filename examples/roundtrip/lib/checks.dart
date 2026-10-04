import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';

import 'package:roundtrip/golang/golang.dart' as golang;

// The round trip checks: each sends values between Dart and the real Go
// library (golang/api.go) and checks them on the side that receives them.
// The app runs them on screen, integration_test/app_test.dart runs them as a
// test on desktop and web.
//
// A check with a note documents a known limitation: it passes when the
// value arrives the way the note describes, so a change in either
// direction shows up.

/// One check. [run] throws when it fails and may return a detail, e.g. a
/// timing, to show next to the result.
class Check {
  const Check(this.group, this.name, this.run, {this.note});

  final String group;
  final String name;
  final String? note;
  final Future<String?> Function() run;
}

/// The outcome of a [Check].
class CheckResult {
  const CheckResult(this.check, this.error, this.detail, this.elapsed);

  final Check check;
  final String? error;
  final String? detail;
  final Duration elapsed;

  bool get passed => error == null;
}

/// A check found a difference.
class CheckFailure implements Exception {
  CheckFailure(this.message);

  final String message;

  @override
  String toString() => message;
}

/// Runs [check] with a timeout.
Future<CheckResult> runCheck(Check check) async {
  final watch = Stopwatch()..start();
  try {
    final detail = await check.run().timeout(const Duration(seconds: 20));
    return CheckResult(check, null, detail, watch.elapsed);
  } catch (error) {
    return CheckResult(check, '$error', null, watch.elapsed);
  }
}

/// Where the checks run: the native platform, or web compiled to JavaScript
/// or WebAssembly.
String get platform => kIsWeb
    ? (jsNumbers ? 'web (JavaScript)' : 'web (WebAssembly)')
    : defaultTargetPlatform.name;

/// Whether ints are JavaScript numbers, 64-bit floats with 53 bits for the
/// integer: on the web when compiled to JavaScript, not with --wasm.
final jsNumbers = identical(1, 1.0);

// ---- Expectations ----

/// Fails unless [got] and [want] are equal, lists and maps element by
/// element.
void expectEqual(Object? got, Object? want, [String what = 'value']) {
  final diffs = jsonDiff(got, want, what);
  if (diffs.isNotEmpty) throw CheckFailure(diffs.join('\n'));
}

/// The differences between two decoded JSON values (or values made of
/// lists, maps and primitives), one line per path.
List<String> jsonDiff(Object? got, Object? want, [String path = 'value']) {
  if (got is Map && want is Map) {
    return [
      for (final key in {...want.keys, ...got.keys})
        ...jsonDiff(got[key], want[key], '$path.$key'),
    ];
  }
  if (got is List && want is List && got.length == want.length) {
    return [
      for (var i = 0; i < got.length; i++)
        ...jsonDiff(got[i], want[i], '$path[$i]'),
    ];
  }
  if (got is List || want is List || got is Map || want is Map) {
    return ['$path: got $got, want $want'];
  }
  return got == want ? [] : ['$path: got ${show(got)}, want ${show(want)}'];
}

String show(Object? v) => v is String ? jsonEncode(v) : '$v';

/// Fails unless [future] fails with an error whose text contains [part].
Future<String> expectError(Future<Object?> future, String part) async {
  try {
    final value = await future;
    throw CheckFailure('succeeded with $value, want an error with "$part"');
  } on CheckFailure {
    rethrow;
  } catch (error) {
    if (!'$error'.contains(part)) {
      throw CheckFailure('got the error "$error", want one with "$part"');
    }
    return '$error';
  }
}

/// Waits until [condition] holds, at most [timeout].
Future<void> eventually(
  FutureOr<bool> Function() condition,
  String what, {
  Duration timeout = const Duration(seconds: 3),
}) async {
  final end = DateTime.now().add(timeout);
  while (!await condition()) {
    if (DateTime.now().isAfter(end)) throw CheckFailure('timed out: $what');
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
}

String ms(Stopwatch watch) => '${watch.elapsedMilliseconds} ms';

/// The bits of a double in hex, like Go's strconv.FormatUint(bits, 16).
String doubleBits(double v) {
  final data = ByteData(8)..setFloat64(0, v);
  final high = data.getUint32(0), low = data.getUint32(4);
  return high == 0
      ? low.toRadixString(16)
      : high.toRadixString(16) + low.toRadixString(16).padLeft(8, '0');
}

// ---- Values ----

/// The Dart twin of sample.Want in golang/sample/sample.go.
golang.Everything wantEverything() => golang.Everything(
  kind: 'sample',
  version: 3,
  createdBy: 'go',
  bool_: true,
  int_: -42,
  int8: -128,
  uint16: 65535,
  int64: 9007199254740991,
  float32: 0.1,
  float64: -1234.5678,
  string: 'Grüße, 世界 🧑‍🚀',
  color: 'teal',
  level: 'high',
  point: [3, -4],
  bytes: Uint8List.fromList([0, 1, 2, 127, 128, 254, 255]),
  time: DateTime.utc(2024, 2, 29, 18, 29, 59, 125),
  duration: const Duration(minutes: 90, milliseconds: 250),
  addr: '2001:db8::1',
  array: [1, 2, 3],
  strings: ['a', '', 'c'],
  items: [
    const golang.Item(id: 1, title: 'one', done: false),
    const golang.Item(id: 2, title: 'two', done: true),
  ],
  ptrs: [
    const golang.Item(id: 3, title: 'three', done: false),
    null,
  ],
  matrix: [
    [1, 0.5],
    [],
    [-2.25],
  ],
  byName: {
    'x': const golang.Item(id: 4, title: 'x', done: false),
    'y': const golang.Item(id: 5, title: 'y', done: false),
  },
  byID: {-1: 'minus one', 7: 'seven'},
  nested: {
    'empty': [],
    'primes': [2, 3, 5],
  },
  page: const golang.PageSampleItem(
    items: [golang.Item(id: 6, title: 'six', done: false)],
    next: 'page-2',
  ),
  child: const golang.Item(id: 8, title: 'child', done: false),
  any: {
    'list': [1, 'two', null, true],
  },
  optional: 'set',
  quoted: 9007199254740991,
  untagged: 'untagged',
);

/// What the Go zero value of sample.Everything looks like in Dart: nil
/// slices and maps are empty, nil pointers and omitted fields null.
golang.Everything zeroEverything() => golang.Everything(
  kind: '',
  version: 0,
  bool_: false,
  int_: 0,
  int8: 0,
  uint16: 0,
  int64: 0,
  float32: 0,
  float64: 0,
  string: '',
  color: '',
  level: 'low',
  point: [0, 0],
  bytes: Uint8List(0),
  time: DateTime.utc(1),
  duration: Duration.zero,
  addr: '',
  array: [0, 0, 0],
  strings: [],
  items: [],
  ptrs: [],
  matrix: [],
  byName: {},
  byID: {},
  nested: {},
  page: const golang.PageSampleItem(items: [], next: null),
  quoted: 0,
  untagged: '',
);

const unicodeText =
    'Grüße 世界 𝄞 🧑‍🚀 \u2028 </script> & "quote" \\ \t\n\x00\x1f';

// ---- The Dart side of golang.Host ----

/// Implements the Go interface Host, which Go calls in the "Go calls Dart"
/// checks.
class CheckHost implements golang.Host {
  final notes = <String>[];

  @override
  String greet(String name) => 'Hello $name, from Dart!';

  @override
  void fail(String message) => throw StateError(message);

  @override
  Future<void> wait(Duration d) => Future<void>.delayed(d);

  @override
  void note(String text) => notes.add(text);
}

final host = CheckHost();

/// Registers the Dart handlers Go calls. Call it before running checks.
void registerHandlers() {
  golang.Host.register(host);
  golang.handlePrompt((question) => question.toUpperCase());
}

// ---- The checks ----

final checks = <Check>[
  // Types
  Check('Types', 'Go → Dart: every type', () async {
    final diffs = jsonDiff(
      (await golang.sample()).toJson(),
      wantEverything().toJson(),
      'Everything',
    );
    if (diffs.isNotEmpty) throw CheckFailure(diffs.join('\n'));
    return null;
  }),
  Check('Types', 'Dart → Go: every type', () async {
    final diffs = await golang.check(wantEverything());
    if (diffs.isNotEmpty) throw CheckFailure(diffs.join('\n'));
    return null;
  }),
  Check('Types', 'Dart → Go → Dart: every type', () async {
    expectEqual(
      (await golang.echo(wantEverything())).toJson(),
      wantEverything().toJson(),
      'Everything',
    );
    return null;
  }),
  Check('Types', 'Go → Dart: zero value', () async {
    expectEqual(
      (await golang.zero()).toJson(),
      zeroEverything().toJson(),
      'Everything',
    );
    return null;
  }),
  Check('Types', 'Dart → Go: zero value', () async {
    final diffs = await golang.checkZero(zeroEverything());
    if (diffs.isNotEmpty) throw CheckFailure(diffs.join('\n'));
    return null;
  }),
  Check('Types', 'nil pointer result is null', () async {
    expectEqual(await golang.nothing(), null);
    return null;
  }),
  Check(
    'Types',
    'nil slices and maps',
    note:
        'JSON null becomes an empty list or map in Dart, so Go gets them '
        'back empty, not nil',
    () async {
      final lists = await golang.nilLists();
      expectEqual(lists.nilSlice, [], 'nilSlice');
      expectEqual(lists.nilMap, {}, 'nilMap');
      expectEqual(lists.nilBytes, [], 'nilBytes');
      expectEqual(await golang.whichNil(lists), {
        'nilSlice': false,
        'emptySlice': false,
        'nilMap': false,
        'nilBytes': false,
      }, 'nil in Go');
      return null;
    },
  ),

  // Numbers
  Check(
    'Numbers',
    'int64 max Go → Dart',
    note: jsNumbers
        ? 'JavaScript numbers have 53 bits, it arrives as 2^63'
        : null,
    () async {
      expectEqual(
        '${await golang.int64Max()}',
        jsNumbers ? '9223372036854776000' : '9223372036854775807',
      );
      return null;
    },
  ),
  Check('Numbers', 'int64 min Go → Dart', () async {
    // -2^63 is exact as a JavaScript number, only printed shorter.
    expectEqual(
      '${await golang.int64Min()}',
      jsNumbers ? '-9223372036854776000' : '-9223372036854775808',
    );
    return null;
  }),
  Check(
    'Numbers',
    'uint64 max Go → Dart',
    note: jsNumbers
        ? 'JavaScript numbers have 53 bits, it arrives rounded'
        : 'Dart ints have 64 bits with a sign, the call fails',
    () async {
      if (jsNumbers) {
        expectEqual('${await golang.uint64Max()}', '18446744073709552000');
        return null;
      }
      return expectError(golang.uint64Max(), 'is not a subtype of type');
    },
  ),
  Check(
    'Numbers',
    '2^53 + 1 Dart → Go',
    note: jsNumbers ? 'JavaScript numbers have 53 bits, Go gets 2^53' : null,
    () async {
      expectEqual(
        await golang.formatInt(int.parse('9007199254740993')),
        jsNumbers ? '9007199254740992' : '9007199254740993',
      );
      return null;
    },
  ),
  Check('Numbers', 'float64 Go → Dart', () async {
    final got = await golang.floats();
    final want = [0.1, 0.1 + 0.2, 1e308, 5e-324, 2.5e-8, 123456789.123, 1.0];
    expectEqual(got.sublist(0, want.length).map(doubleBits).toList(), [
      for (final v in want) doubleBits(v),
    ], 'bits');
    return null;
  }),
  Check('Numbers', 'float64 Dart → Go', () async {
    final values = [0.1, 0.1 + 0.2, 1e308, 5e-324, 2.5e-8, 123456789.123, 1.0];
    expectEqual(await golang.formatFloats(values), [
      for (final v in values) doubleBits(v),
    ], 'bits in Go');
    return null;
  }),
  Check(
    'Numbers',
    '-0.0 Go → Dart',
    note: jsNumbers ? null : 'Dart reads the JSON number -0 as the int 0',
    () async {
      final got = (await golang.floats()).last;
      expectEqual(doubleBits(got), doubleBits(jsNumbers ? -0.0 : 0.0), 'bits');
      return null;
    },
  ),
  Check('Numbers', '-0.0 Dart → Go', () async {
    expectEqual(await golang.formatFloats([-0.0]), [
      doubleBits(-0.0),
    ], 'bits in Go');
    return null;
  }),
  Check('Numbers', 'NaN fails in Go', () async {
    return expectError(golang.notANumber(), 'unsupported value: NaN');
  }),
  Check('Numbers', 'NaN fails in Dart', () async {
    return expectError(golang.formatFloats([double.nan]), 'NaN');
  }),

  // Strings and bytes
  Check('Strings', 'Unicode Go → Dart', () async {
    expectEqual(await golang.unicode(), unicodeText);
    return null;
  }),
  Check('Strings', 'Unicode Dart → Go', () async {
    final info = await golang.describeText(unicodeText);
    expectEqual(
      [info.bytes, info.runes, info.valid],
      [utf8.encode(unicodeText).length, unicodeText.runes.length, true],
      '[bytes, runes, valid]',
    );
    return null;
  }),
  Check(
    'Strings',
    'invalid UTF-8 Go → Dart',
    note: 'encoding/json replaces invalid bytes with U+FFFD',
    () async {
      expectEqual(await golang.invalidUTF8(), 'a\uFFFDb');
      return null;
    },
  ),
  Check(
    'Strings',
    'lone surrogate Dart → Go',
    note: 'Go strings are UTF-8, it arrives as U+FFFD',
    () async {
      final info = await golang.describeText('a\uD800b');
      expectEqual(info.quote, r'"a\ufffdb"', 'in Go');
      return null;
    },
  ),
  Check('Strings', 'every byte Go → Dart', () async {
    expectEqual(await golang.allBytes(), List.generate(256, (i) => i));
    return null;
  }),
  Check('Strings', 'every byte Dart → Go', () async {
    final bytes = Uint8List.fromList(List.generate(256, (i) => i));
    final info = await golang.describeBytes(bytes);
    var sum = 0;
    for (var i = 0; i < bytes.length; i++) {
      sum += bytes[i] * (i % 251 + 1);
    }
    expectEqual(
      [info.len, info.sum, info.first, info.last],
      [256, sum, 0, 255],
      '[len, sum, first, last]',
    );
    return null;
  }),
  Check('Strings', 'empty bytes Dart → Go', () async {
    final info = await golang.describeBytes(Uint8List(0));
    expectEqual([info.len, info.first], [0, -1], '[len, first]');
    return null;
  }),
  Check('Strings', '8 MiB of bytes there and back', () async {
    final bytes = Uint8List(8 << 20);
    for (var i = 0; i < bytes.length; i++) {
      bytes[i] = (i * 31 + 7) & 0xff;
    }
    final watch = Stopwatch()..start();
    final back = await golang.echoBytes(bytes);
    watch.stop();
    expectEqual(back.length, bytes.length, 'length');
    for (var i = 0; i < bytes.length; i++) {
      if (back[i] != bytes[i]) throw CheckFailure('byte $i differs');
    }
    return ms(watch);
  }),
  Check('Strings', '1M characters Go → Dart', () async {
    final watch = Stopwatch()..start();
    final text = await golang.repeat('ab€', 1 << 18);
    watch.stop();
    expectEqual(text.length, 3 << 18, 'length');
    expectEqual(text.substring(text.length - 3), 'ab€', 'end');
    return ms(watch);
  }),

  // Time
  Check(
    'Time',
    'nanoseconds Go → Dart',
    note:
        'Dart has microseconds, the rest is cut off; the zone offset '
        'becomes UTC',
    () async {
      final got = await golang.preciseTime();
      expectEqual(got.isUtc, true, 'isUtc');
      expectEqual(got.toIso8601String(), '2024-02-29T18:29:59.123456Z');
      return null;
    },
  ),
  Check('Time', 'Dart → Go', () async {
    final local = DateTime(2024, 7, 1, 12, 30);
    expectEqual(await golang.formatTime(local), _goUtc(local.toUtc()), 'local');
    final utc = DateTime.utc(2024, 2, 29, 18, 29, 59, 123, 456);
    expectEqual(
      await golang.formatTime(utc),
      '2024-02-29T18:29:59.123456Z',
      'utc',
    );
    return null;
  }),
  Check(
    'Time',
    'durations Go → Dart',
    note:
        'Duration has microseconds, the nanoseconds are cut off'
        '${jsNumbers ? '; above 2^53 ns it is rounded' : ''}',
    () async {
      final got = await golang.durations();
      expectEqual(
        [for (final d in got) '${d.inMicroseconds}'],
        [
          '1500000',
          '-1000000',
          jsNumbers ? '9223372036854776' : '9223372036854775',
        ],
        'microseconds',
      );
      return null;
    },
  ),
  Check('Time', 'durations Dart → Go', () async {
    expectEqual(
      await golang.formatDurations([
        const Duration(microseconds: 1500001),
        const Duration(seconds: -1),
      ]),
      ['1500001000', '-1000000000'],
      'nanoseconds in Go',
    );
    return null;
  }),

  // Errors
  Check('Errors', 'Go error fails the call', () async {
    try {
      await golang.fail('boom');
    } on golang.GolangException catch (e) {
      expectEqual([e.channel, e.message], ['Fail', 'boom']);
      return null;
    }
    throw CheckFailure('no GolangException');
  }),
  Check('Errors', 'Go panic fails the call', () async {
    return expectError(
      golang.panic('oops'),
      'Go handler "Panic" panicked: oops',
    );
  }),
  Check('Errors', 'too few arguments', () async {
    return expectError(
      golang.emitter.request<Object?>('Add', [1]),
      'got 1 arguments, want 2',
    );
  }),
  Check('Errors', 'argument of the wrong type', () async {
    return expectError(
      golang.emitter.request<Object?>('Add', ['1', 2]),
      'invalid argument 1',
    );
  }),
  Check('Errors', 'unknown channel', () async {
    return expectError(
      golang.emitter.request<Object?>('Missing'),
      'no Go handler for channel "Missing"',
    );
  }),

  // Go calls Dart
  Check('Go calls Dart', 'request inside a request', () async {
    expectEqual(await golang.askHost('Ada'), 'Hello Ada, from Dart!');
    return null;
  }),
  Check('Go calls Dart', 'Dart error reaches Go', () async {
    return expectError(
      golang.askHostToFail('nope'),
      'dart handler "Host.Fail": Bad state: nope',
    );
  }),
  Check('Go calls Dart', 'Go stops waiting on its timeout', () async {
    final watch = Stopwatch()..start();
    await expectError(
      golang.askHostWithTimeout(
        const Duration(seconds: 2),
        const Duration(milliseconds: 100),
      ),
      'deadline exceeded',
    );
    if (watch.elapsedMilliseconds > 1500) {
      throw CheckFailure('Go waited ${ms(watch)}');
    }
    return ms(watch);
  }),
  Check('Go calls Dart', 'notifications arrive in order', () async {
    host.notes.clear();
    await golang.noteHost(['a', 'b', 'c']);
    await eventually(() => host.notes.length >= 3, 'three notes');
    expectEqual(host.notes, ['a', 'b', 'c']);
    return null;
  }),
  Check('Go calls Dart', 'raw channel request (fluffy.Request)', () async {
    expectEqual(await golang.prompt('yes?'), 'YES?');
    return null;
  }),
  Check('Go calls Dart', '10 000 emits arrive in order', () async {
    final events = golang.event().take(10000).toList();
    final watch = Stopwatch()..start();
    await golang.emitEvents(10000);
    final got = await events.timeout(const Duration(seconds: 10));
    watch.stop();
    for (var i = 0; i < got.length; i++) {
      if (got[i].seq != i) throw CheckFailure('event $i has seq ${got[i].seq}');
    }
    return ms(watch);
  }),

  // Streams
  Check('Streams', 'ends when Go closes the channel', () async {
    expectEqual(await golang.count(5).toList(), [0, 1, 2, 3, 4]);
    return null;
  }),
  Check('Streams', '10 000 values in order', () async {
    final watch = Stopwatch()..start();
    final got = await golang.count(10000).toList();
    watch.stop();
    expectEqual(got.length, 10000, 'length');
    for (var i = 0; i < got.length; i++) {
      if (got[i] != i) throw CheckFailure('value $i is ${got[i]}');
    }
    return ms(watch);
  }),
  Check('Streams', 'Go error fails the stream', () async {
    return expectError(golang.broken().toList(), 'broken stream');
  }),
  Check('Streams', 'nil channel ends right away', () async {
    expectEqual(await golang.empty().toList(), []);
    return null;
  }),
  Check('Streams', 'cancel ends the Go context', () async {
    final before = await golang.stats();
    expectEqual(await golang.ticks().take(3).toList(), [0, 1, 2]);
    await eventually(() async {
      final now = await golang.stats();
      return now.active == before.active &&
          now.cancelled == before.cancelled + 1;
    }, 'Go saw the cancel');
    return null;
  }),

  // Concurrency
  Check('Concurrency', '1000 concurrent requests', () async {
    final watch = Stopwatch()..start();
    final sums = await Future.wait([
      for (var i = 0; i < 1000; i++) golang.add(i, i),
    ]);
    watch.stop();
    for (var i = 0; i < sums.length; i++) {
      if (sums[i] != 2 * i) throw CheckFailure('request $i got ${sums[i]}');
    }
    return ms(watch);
  }),
  Check('Concurrency', 'handlers run in parallel', () async {
    final watch = Stopwatch()..start();
    await Future.wait([for (var i = 0; i < 20; i++) golang.sleep(200)]);
    watch.stop();
    if (watch.elapsedMilliseconds > 1500) {
      throw CheckFailure('20 × 200 ms took ${ms(watch)}');
    }
    return ms(watch);
  }),
];

/// How Go formats a whole-second UTC time with RFC3339Nano.
String _goUtc(DateTime t) => t.toIso8601String().replaceFirst('.000Z', 'Z');
