import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import 'package:mandelbrot/bench.dart';
import 'package:mandelbrot/golang/golang.dart' as golang;
import 'package:mandelbrot/main.dart';

// Against the real Go library. On desktop:
//
//   fvm flutter test integration_test -d linux
//
// On the web, after "fluffy lib web", with chromedriver --port=4444 running:
//
//   fvm flutter drive --driver=test_driver/integration_test.dart \
//     --target=integration_test/app_test.dart -d web-server --browser-name=chrome
//
// The benchmark results end up in build/integration_response_data.json.
void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  Future<void> until(
    WidgetTester tester,
    Future<bool> Function() condition,
    String what,
  ) async {
    final end = DateTime.now().add(const Duration(seconds: 20));
    while (!await tester.runAsync(condition).then((ok) => ok ?? false)) {
      if (DateTime.now().isAfter(end)) fail('timed out: $what');
      await tester.pump(const Duration(milliseconds: 50));
    }
  }

  testWidgets('Go renders every row', (tester) async {
    final tiles = (await tester.runAsync(
      () => golang.fractal
          .render(
            const golang.View(
              x: -0.75,
              y: 0,
              scale: 3.5 / 300,
              width: 300,
              height: 200,
              maxIter: 300,
              workers: 0,
            ),
          )
          .toList(),
    ))!;
    final rows = {
      for (final t in tiles)
        for (var y = t.y; y < t.y + t.height; y++) y,
    };
    expect(rows, hasLength(200));
    expect(tiles.last.done, tiles.last.total);
  });

  testWidgets('cancelling the stream stops the Go workers', (tester) async {
    final before = (await tester.runAsync(golang.fractal.stats))!;
    await tester.runAsync(() async {
      // Deep in the set with many iterations: seconds of work.
      final render = golang.fractal.render(
        const golang.View(
          x: -0.743643887037151,
          y: 0.13182590420533,
          scale: 1e-11,
          width: 1000,
          height: 1000,
          maxIter: 100000,
          workers: 2,
        ),
      );
      await render.first; // first cancels the subscription
    });
    await until(tester, () async {
      final now = await golang.fractal.stats();
      return now.workers == 0 && now.cancelled == before.cancelled + 1;
    }, 'the Go workers stopped');
  });

  testWidgets('the app renders and zooms', (tester) async {
    await tester.pumpWidget(const MandelbrotApp());
    String timing() =>
        tester.widget<Text>(find.byKey(const Key('timing'))).data!;
    bool finished() {
      final m = RegExp(r'^(\d+) of (\d+) tiles').firstMatch(timing());
      return m != null && m[1] == m[2] && m[1] != '0';
    }

    await until(tester, () async => finished(), 'the first render');
    await tester.tapAt(tester.getCenter(find.byType(RawImage)));
    await tester.pump();
    await until(tester, () async => finished(), 'the zoomed render');
    expect(find.textContaining('2.0×'), findsOneWidget);
  });

  testWidgets('bridge benchmarks', (tester) async {
    final results = <String, String>{};
    for (final benchmark in benchmarks) {
      final m = await tester.runAsync(() => benchmark.run(quick: true));
      results[benchmark.name] = '$m';
      // ignore: avoid_print
      print('${benchmark.name.padRight(24)} $m');
    }
    binding.reportData = {'benchmarks': results};
  });
}
