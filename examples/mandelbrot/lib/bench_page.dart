import 'package:flutter/material.dart';

import 'package:mandelbrot/bench.dart';

/// Runs the bridge benchmarks (lib/bench.dart) and lists the results.
class BenchPage extends StatefulWidget {
  const BenchPage({super.key});

  @override
  State<BenchPage> createState() => _BenchPageState();
}

class _BenchPageState extends State<BenchPage> {
  final _results = <Benchmark, String>{};
  Benchmark? _running;

  Future<void> _runAll() async {
    setState(_results.clear);
    for (final benchmark in benchmarks) {
      setState(() => _running = benchmark);
      String result;
      try {
        result = '${await benchmark.run()}';
      } catch (e) {
        result = 'failed: $e';
      }
      if (!mounted) return;
      setState(() => _results[benchmark] = result);
    }
    setState(() => _running = null);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _running == null ? _runAll : null,
        icon: const Icon(Icons.speed),
        label: const Text('Run benchmarks'),
      ),
      body: ListView(
        padding: const EdgeInsets.only(bottom: 88),
        children: [
          const ListTile(
            title: Text('What the bridge costs'),
            subtitle: Text(
              'Go does nothing in these calls, so the time is spent in '
              'fluffy: JSON encoding (bytes as base64), copying and the '
              'hop between Dart and Go.',
            ),
          ),
          for (final benchmark in benchmarks)
            ListTile(
              title: Text(benchmark.name),
              subtitle: Text(benchmark.description),
              trailing: _running == benchmark
                  ? const SizedBox.square(
                      dimension: 20,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : Text(_results[benchmark] ?? ''),
            ),
        ],
      ),
    );
  }
}
