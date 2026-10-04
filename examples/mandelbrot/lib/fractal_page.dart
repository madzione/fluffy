import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

import 'package:mandelbrot/fractal.dart';
import 'package:mandelbrot/golang/golang.dart' as golang;

/// The Mandelbrot set, rendered by Go while the UI stays responsive. Tap
/// to zoom in, secondary tap or long press to zoom out, drag to move, use
/// the mouse wheel to zoom. Every change cancels the running render.
class FractalPage extends StatefulWidget {
  const FractalPage({super.key, required this.controller});

  final FractalController controller;

  @override
  State<FractalPage> createState() => _FractalPageState();
}

class _FractalPageState extends State<FractalPage> {
  FractalController get _fractal => widget.controller;

  golang.Info? _info;
  golang.Stats? _stats;
  Timer? _poll;
  Offset _drag = Offset.zero;
  double _ratio = 1;

  @override
  void initState() {
    super.initState();
    golang.fractal.info().then((info) {
      if (mounted) setState(() => _info = info);
    });
    // The Go counters show the cancelled renders and that their workers
    // really stop.
    _poll = Timer.periodic(const Duration(milliseconds: 250), (_) async {
      final stats = await golang.fractal.stats();
      if (mounted) setState(() => _stats = stats);
    });
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  void _zoom(Offset local, double factor) =>
      _fractal.zoom(factor, local.dx * _ratio, local.dy * _ratio);

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        // Render in device pixels, at most about 4 megapixels.
        final dpr = MediaQuery.devicePixelRatioOf(context);
        final area = constraints.maxWidth * constraints.maxHeight * dpr * dpr;
        _ratio = area > 4e6 ? dpr * math.sqrt(4e6 / area) : dpr;
        final width = (constraints.maxWidth * _ratio).round();
        final height = (constraints.maxHeight * _ratio).round();
        if (width > 0 && height > 0) {
          scheduleMicrotask(() => _fractal.resize(width, height));
        }

        return Stack(
          fit: StackFit.expand,
          children: [
            Listener(
              onPointerSignal: (event) {
                if (event is PointerScrollEvent) {
                  _zoom(
                    event.localPosition,
                    math.pow(1.002, -event.scrollDelta.dy).toDouble(),
                  );
                }
              },
              child: GestureDetector(
                onTapUp: (d) => _zoom(d.localPosition, 2),
                onSecondaryTapUp: (d) => _zoom(d.localPosition, 0.5),
                onLongPressStart: (d) => _zoom(d.localPosition, 0.5),
                onPanUpdate: (d) => setState(() => _drag += d.delta),
                onPanEnd: (_) {
                  final drag = _drag * _ratio;
                  setState(() => _drag = Offset.zero);
                  _fractal.pan(drag.dx, drag.dy);
                },
                child: ColoredBox(
                  color: Colors.black,
                  child: ListenableBuilder(
                    listenable: _fractal,
                    builder: (context, _) => Transform.translate(
                      offset: _drag,
                      child: RawImage(
                        image: _fractal.image,
                        fit: BoxFit.fill,
                        filterQuality: FilterQuality.medium,
                      ),
                    ),
                  ),
                ),
              ),
            ),
            Positioned(
              left: 12,
              top: 12,
              child: ListenableBuilder(
                listenable: _fractal,
                builder: (context, _) =>
                    _InfoCard(fractal: _fractal, info: _info, stats: _stats),
              ),
            ),
            Positioned(
              left: 12,
              right: 12,
              bottom: 12,
              child: ListenableBuilder(
                listenable: _fractal,
                builder: (context, _) =>
                    _Controls(fractal: _fractal, cpus: _info?.goMaxProcs ?? 1),
              ),
            ),
          ],
        );
      },
    );
  }
}

class _InfoCard extends StatelessWidget {
  const _InfoCard({required this.fractal, this.info, this.stats});

  final FractalController fractal;
  final golang.Info? info;
  final golang.Stats? stats;

  @override
  Widget build(BuildContext context) {
    final info = this.info, stats = this.stats;
    final progress = fractal.total == 0 ? null : fractal.done / fractal.total;
    return Card(
      color: Colors.black54,
      child: DefaultTextStyle(
        style: const TextStyle(color: Colors.white, fontSize: 13),
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: SizedBox(
            width: 310,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (info != null)
                  Text(
                    'Go on ${info.platform}, ${info.goMaxProcs} of '
                    '${info.cpus} CPUs',
                  ),
                Text(
                  '${fractal.width} × ${fractal.height} px, '
                  '${formatZoom(fractal.magnification)}, '
                  '${fractal.maxIter} iterations',
                ),
                const SizedBox(height: 8),
                LinearProgressIndicator(
                  value: fractal.rendering ? progress : 1,
                ),
                const SizedBox(height: 8),
                Text(
                  key: const Key('timing'),
                  '${fractal.done} of ${fractal.total} tiles in '
                  '${fractal.elapsed.inMilliseconds} ms',
                ),
                Text(
                  'Go workers busy ${fractal.goTime.inMilliseconds} ms, '
                  '${fractal.parallelism.toStringAsFixed(1)} at once',
                ),
                if (stats != null)
                  Text(
                    key: const Key('stats'),
                    'Renders: ${stats.finished} done, '
                    '${stats.cancelled} cancelled, '
                    '${stats.workers} workers running',
                  ),
                if (fractal.error case final error?)
                  Text(error, style: const TextStyle(color: Colors.redAccent)),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _Controls extends StatelessWidget {
  const _Controls({required this.fractal, required this.cpus});

  final FractalController fractal;
  final int cpus;

  @override
  Widget build(BuildContext context) {
    final workerChoices = {
      0,
      for (var n = 1; n < cpus; n *= 2) n,
      cpus,
    }.toList()..sort();
    return Card(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
        child: Row(
          children: [
            const Text('Iterations'),
            Expanded(
              child: Slider(
                // Logarithmic from 100 to 20 000.
                value: math.log(fractal.maxIter) / math.ln10,
                min: 2,
                max: math.log(20000) / math.ln10,
                label: '${fractal.maxIter}',
                divisions: 46,
                onChanged: (v) {
                  fractal.maxIter = math.pow(10, v).round();
                  fractal.render();
                },
              ),
            ),
            const SizedBox(width: 12),
            const Text('Workers '),
            DropdownButton<int>(
              value: workerChoices.contains(fractal.workers)
                  ? fractal.workers
                  : 0,
              items: [
                for (final n in workerChoices)
                  DropdownMenuItem(
                    value: n,
                    child: Text(n == 0 ? 'one per CPU' : '$n'),
                  ),
              ],
              onChanged: (n) {
                fractal.workers = n ?? 0;
                fractal.render();
              },
            ),
            const SizedBox(width: 12),
            IconButton(
              tooltip: 'Reset',
              icon: const Icon(Icons.home),
              onPressed: fractal.reset,
            ),
          ],
        ),
      ),
    );
  }
}
