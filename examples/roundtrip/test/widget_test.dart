import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

import 'package:roundtrip/checks.dart';
import 'package:roundtrip/golang/golang.dart' as golang;
import 'package:roundtrip/main.dart';

// Checks the generated Dart classes without Go: golang/sample/testdata/
// want.json is sample.Want as Go encodes it (go test ./sample -update
// rewrites it).
void main() {
  test('the generated classes decode what Go encodes', () {
    final json = jsonDecode(
      File('golang/sample/testdata/want.json').readAsStringSync(),
    );
    expectEqual(
      golang.Everything.fromJson(json).toJson(),
      wantEverything().toJson(),
      'Everything',
    );
  });

  test('the generated classes encode what Go decodes', () {
    final json = jsonDecode(
      File('golang/sample/testdata/want.json').readAsStringSync(),
    ) as Map<String, dynamic>;
    final encoded = jsonDecode(
      jsonEncode(wantEverything().toJson()),
    ) as Map<String, dynamic>;
    // Go writes the time with its zone, Dart in UTC.
    expect(encoded.remove('time'), '2024-02-29T18:29:59.125Z');
    expect(json.remove('time'), '2024-02-29T23:59:59.125+05:30');
    expect(encoded, json);
  });

  testWidgets('the app lists the checks', (tester) async {
    golang.Bridge.fake();
    await tester.pumpWidget(const RoundtripApp(autorun: false));
    await tester.pump();
    expect(find.text('Go → Dart: every type'), findsOneWidget);
    expect(find.text('Run all'), findsOneWidget);
  });
}
