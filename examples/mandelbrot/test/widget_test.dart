import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';

import 'package:mandelbrot/fractal.dart';
import 'package:mandelbrot/golang/golang.dart' as golang;

// The controller against Bridge.fake(): the fake Go side serves the stream
// fractal.Render with receiver.stream, the way a Go channel would.
void main() {
  test(
    'the controller assembles the tiles and cancels the old render',
    () async {
      final go = golang.Bridge.fake();
      final views = <golang.View>[];
      var cancelled = 0;
      // Two-row tiles of one value per render, sent bottom up.
      Stream<Object?> render(golang.View view, int value) async* {
        final total = view.height ~/ 2;
        var finished = false;
        try {
          for (var band = total - 1; band >= 0; band--) {
            await Future<void>.delayed(Duration.zero);
            yield golang.Tile(
              y: band * 2,
              height: 2,
              pixels: Uint8List(view.width * 2 * 4)
                ..fillRange(0, view.width * 8, value),
              done: total - band,
              total: total,
              micros: 100,
            ).toJson();
          }
          finished = true;
        } finally {
          if (!finished) cancelled++;
        }
      }

      go.receiver.stream('fractal.Render', (data) {
        final view = golang.View.fromJson((data as List<dynamic>)[0]);
        views.add(view);
        return render(view, views.length);
      });

      final fractal = FractalController();
      fractal.resize(4, 6);
      fractal.zoom(2, 2, 3); // cancels the first render
      await Future<void>.delayed(const Duration(milliseconds: 50));

      expect(views, hasLength(2));
      expect(views.last.scale, closeTo(views.first.scale / 2, 1e-12));
      expect(cancelled, 1);
      expect([fractal.done, fractal.total, fractal.rendering], [3, 3, false]);
      expect(fractal.pixels.toSet(), {
        2,
      }, reason: 'every pixel from the second render');
      expect(fractal.goTime, const Duration(microseconds: 300));
    },
  );

  test('formatZoom', () {
    expect(formatZoom(1), '1.0×');
    expect(formatZoom(2500), '2.5 k×');
    expect(formatZoom(3.2e7), '32.0 M×');
  });
}
