import 'package:flutter/material.dart';

import 'checks.dart';

// Runs the round trip checks (lib/checks.dart) against the Go library and
// shows the results, grouped like the checks.

void main() {
  runApp(const RoundtripApp());
}

class RoundtripApp extends StatelessWidget {
  const RoundtripApp({super.key, this.autorun = true});

  /// Whether the checks run when the app starts.
  final bool autorun;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'fluffy round trip',
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(seedColor: Colors.cyan),
      ),
      home: ChecksPage(autorun: autorun),
    );
  }
}

class ChecksPage extends StatefulWidget {
  const ChecksPage({super.key, required this.autorun});

  final bool autorun;

  @override
  State<ChecksPage> createState() => _ChecksPageState();
}

class _ChecksPageState extends State<ChecksPage> {
  final _results = <Check, CheckResult>{};
  Check? _running;

  @override
  void initState() {
    super.initState();
    registerHandlers();
    if (widget.autorun) _runAll();
  }

  Future<void> _runAll() async {
    if (_running != null) return;
    setState(_results.clear);
    for (final check in checks) {
      setState(() => _running = check);
      final result = await runCheck(check);
      if (!mounted) return;
      setState(() => _results[check] = result);
    }
    setState(() => _running = null);
  }

  Future<void> _runOne(Check check) async {
    if (_running != null) return;
    setState(() => _running = check);
    final result = await runCheck(check);
    if (!mounted) return;
    setState(() {
      _results[check] = result;
      _running = null;
    });
  }

  @override
  Widget build(BuildContext context) {
    final failed = _results.values.where((r) => !r.passed).length;
    final passed = _results.length - failed;
    final groups = <String, List<Check>>{};
    for (final check in checks) {
      groups.putIfAbsent(check.group, () => []).add(check);
    }

    return Scaffold(
      appBar: AppBar(
        backgroundColor: Theme.of(context).colorScheme.inversePrimary,
        title: Text('Round trip on $platform'),
        actions: [
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Center(
              child: Text(
                '$passed passed, $failed failed of ${checks.length}',
                key: const Key('summary'),
              ),
            ),
          ),
        ],
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _running == null ? _runAll : null,
        icon: const Icon(Icons.replay),
        label: const Text('Run all'),
      ),
      body: ListView(
        padding: const EdgeInsets.only(bottom: 88),
        children: [
          for (final MapEntry(key: group, value: members)
              in groups.entries) ...[
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
              child: Text(
                group,
                style: Theme.of(context).textTheme.titleMedium,
              ),
            ),
            for (final check in members)
              _CheckTile(
                check: check,
                result: _results[check],
                running: _running == check,
                onTap: () => _runOne(check),
              ),
          ],
        ],
      ),
    );
  }
}

class _CheckTile extends StatelessWidget {
  const _CheckTile({
    required this.check,
    required this.result,
    required this.running,
    required this.onTap,
  });

  final Check check;
  final CheckResult? result;
  final bool running;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final result = this.result;
    final Widget icon = switch (result) {
      _ when running => const SizedBox.square(
        dimension: 20,
        child: CircularProgressIndicator(strokeWidth: 2),
      ),
      null => Icon(Icons.circle_outlined, color: colors.outline),
      CheckResult(passed: true) => const Icon(
        Icons.check_circle,
        color: Colors.green,
      ),
      _ => Icon(Icons.error, color: colors.error),
    };
    final lines = [
      ?result?.error,
      if (check.note case final note?) 'Limitation: $note',
    ];

    return ListTile(
      dense: true,
      leading: icon,
      title: Text(check.name),
      subtitle: lines.isEmpty ? null : Text(lines.join('\n')),
      trailing: result == null
          ? null
          : Text(result.detail ?? '${result.elapsed.inMilliseconds} ms'),
      onTap: onTap,
    );
  }
}
