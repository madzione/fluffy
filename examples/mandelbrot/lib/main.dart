import 'package:flutter/material.dart';

import 'package:mandelbrot/bench_page.dart';
import 'package:mandelbrot/fractal.dart';
import 'package:mandelbrot/fractal_page.dart';

// Heavy work in Go: the Mandelbrot set is rendered by worker goroutines
// (golang/fractal) and streamed to Dart tile by tile, cancelled whenever
// the view changes. The second tab measures the bridge itself.

void main() {
  runApp(const MandelbrotApp());
}

class MandelbrotApp extends StatefulWidget {
  const MandelbrotApp({super.key});

  @override
  State<MandelbrotApp> createState() => _MandelbrotAppState();
}

class _MandelbrotAppState extends State<MandelbrotApp> {
  final _fractal = FractalController();
  var _tab = 0;

  @override
  void dispose() {
    _fractal.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Mandelbrot',
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(seedColor: Colors.indigo),
      ),
      home: Scaffold(
        body: IndexedStack(
          index: _tab,
          children: [
            FractalPage(controller: _fractal),
            const BenchPage(),
          ],
        ),
        bottomNavigationBar: NavigationBar(
          selectedIndex: _tab,
          onDestinationSelected: (tab) => setState(() => _tab = tab),
          destinations: const [
            NavigationDestination(icon: Icon(Icons.blur_on), label: 'Fractal'),
            NavigationDestination(icon: Icon(Icons.speed), label: 'Bridge'),
          ],
        ),
      ),
    );
  }
}
