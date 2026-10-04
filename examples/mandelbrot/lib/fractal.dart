import 'dart:async';
import 'dart:math' as math;
import 'dart:ui' as ui;

import 'package:flutter/foundation.dart';

import 'package:mandelbrot/golang/golang.dart' as golang;

/// Renders the Mandelbrot set with Go (golang/fractal) and keeps the image.
///
/// Every change of the view starts a new render and cancels the running
/// one: cancelling the Dart stream ends the Go context, and the Go workers
/// stop after their current row.
class FractalController extends ChangeNotifier {
  static const home = (x: -0.75, y: 0.0, width: 3.5);

  double x = home.x, y = home.y;

  /// The width of one pixel in the complex plane, set by [resize].
  double scale = 0;
  int maxIter = 500;

  /// The number of Go workers, 0 for one per CPU.
  int workers = 0;

  int width = 0, height = 0;

  /// The RGBA pixels, filled in tile by tile.
  Uint8List pixels = Uint8List(0);

  /// The image of [pixels], updated at most once per decode.
  ui.Image? image;

  /// The tiles of the current render.
  int done = 0, total = 0;

  /// Wall time of the current render, from the request to the last tile.
  Duration elapsed = Duration.zero;

  /// The time the Go workers spent on the tiles of the current render.
  Duration goTime = Duration.zero;
  String? error;

  StreamSubscription<golang.Tile>? _render;
  final _watch = Stopwatch();
  bool _decoding = false, _dirty = false, _disposed = false;

  bool get rendering => _render != null;

  /// Sets the size of the image in pixels, keeping the center and the
  /// visible width, and renders.
  void resize(int width, int height) {
    if (width == this.width && height == this.height) return;
    final visible = this.width == 0 ? home.width : scale * this.width;
    this.width = width;
    this.height = height;
    scale = visible / width;
    pixels = Uint8List(width * height * 4);
    render();
  }

  /// Zooms by [factor] (above 1 zooms in) keeping the pixel at [px], [py]
  /// in place.
  void zoom(double factor, double px, double py) {
    final cx = x + (px - width / 2) * scale;
    final cy = y - (py - height / 2) * scale;
    scale /= factor;
    x = cx - (px - width / 2) * scale;
    y = cy + (py - height / 2) * scale;
    render();
  }

  /// Moves the view by [dx], [dy] pixels.
  void pan(double dx, double dy) {
    x -= dx * scale;
    y += dy * scale;
    render();
  }

  void reset() {
    x = home.x;
    y = home.y;
    scale = home.width / width;
    render();
  }

  /// Starts a render of the current view, cancelling the running one.
  void render() {
    if (width == 0 || height == 0) return;
    _render?.cancel();
    done = total = 0;
    goTime = elapsed = Duration.zero;
    error = null;
    _watch
      ..reset()
      ..start();
    final view = golang.View(
      x: x,
      y: y,
      scale: scale,
      width: width,
      height: height,
      maxIter: maxIter,
      workers: workers,
    );
    late final StreamSubscription<golang.Tile> render;
    render = golang.fractal
        .render(view)
        .listen(
          _add,
          onError: (Object e) {
            error = '$e';
            _finish(render);
          },
          onDone: () => _finish(render),
        );
    _render = render;
    notifyListeners();
  }

  void _add(golang.Tile tile) {
    final start = tile.y * width * 4;
    pixels.setRange(start, start + tile.pixels.length, tile.pixels);
    done = tile.done;
    total = tile.total;
    elapsed = _watch.elapsed;
    goTime += Duration(microseconds: tile.micros);
    _updateImage();
  }

  void _finish(StreamSubscription<golang.Tile> render) {
    if (_render != render) return;
    _render = null;
    _watch.stop();
    notifyListeners();
  }

  /// Decodes [pixels] into [image], once more if tiles arrived meanwhile.
  void _updateImage() {
    if (_decoding) {
      _dirty = true;
      return;
    }
    _decoding = true;
    ui.decodeImageFromPixels(pixels, width, height, ui.PixelFormat.rgba8888, (
      decoded,
    ) {
      _decoding = false;
      if (_disposed) {
        decoded.dispose();
        return;
      }
      image?.dispose();
      image = decoded;
      notifyListeners();
      if (_dirty) {
        _dirty = false;
        _updateImage();
      }
    });
  }

  /// How many workers ran at once on average: Go time / wall time.
  double get parallelism => elapsed == Duration.zero
      ? 0
      : goTime.inMicroseconds / elapsed.inMicroseconds;

  /// The zoom relative to the start view.
  double get magnification => width == 0 ? 1 : home.width / (scale * width);

  @override
  void dispose() {
    _disposed = true;
    _render?.cancel();
    image?.dispose();
    super.dispose();
  }
}

/// Formats [magnification] like 1.5×, 2.3 k× or 4.1 M×.
String formatZoom(double magnification) {
  if (magnification < 1000) return '${magnification.toStringAsFixed(1)}×';
  final exp = (math.log(magnification) / math.ln10).floor();
  if (exp < 6) return '${(magnification / 1e3).toStringAsFixed(1)} k×';
  if (exp < 9) return '${(magnification / 1e6).toStringAsFixed(1)} M×';
  return '${(magnification / 1e9).toStringAsFixed(1)} G×';
}
