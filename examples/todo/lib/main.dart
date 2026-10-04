import 'dart:async';

import 'package:flutter/material.dart';

import 'package:todo/golang/golang.dart' as golang;

// The todo list is a Go service (golang/todo/, bound as "todo" in golang/main.go).
// fluffy generate turns its methods into golang.todo.*, its Item struct into
// golang.Item and its //fluffy:dart interface UI into golang.TodoUI.

void main() {
  runApp(const TodoApp());
}

class TodoApp extends StatefulWidget {
  const TodoApp({super.key});

  @override
  State<TodoApp> createState() => _TodoAppState();
}

class _TodoAppState extends State<TodoApp> {
  final _navigator = GlobalKey<NavigatorState>();

  @override
  void initState() {
    super.initState();
    // Go asks through todo.UI before deleting, the dialogs answer.
    golang.TodoUI.register(DialogUI(_navigator));
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Todo',
      navigatorKey: _navigator,
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(seedColor: Colors.cyan),
      ),
      home: const TodoPage(),
    );
  }
}

/// The Dart implementation of the Go interface todo.UI.
class DialogUI implements golang.TodoUI {
  DialogUI(this.navigator);

  final GlobalKey<NavigatorState> navigator;

  @override
  Future<bool> confirm(String question) async {
    final context = navigator.currentContext;
    if (context == null) return false;
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        content: Text(question),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    return ok ?? false;
  }
}

enum Filter { all, open, done }

class TodoPage extends StatefulWidget {
  const TodoPage({super.key});

  @override
  State<TodoPage> createState() => _TodoPageState();
}

class _TodoPageState extends State<TodoPage> {
  // Go sends the whole list now and after every change, from any UI.
  final _items = golang.todo.watch();
  final _version = golang.todo.version();
  final _title = TextEditingController();
  var _filter = Filter.all;

  @override
  void dispose() {
    _title.dispose();
    super.dispose();
  }

  /// Runs a Go call and shows its error, e.g. "the title is empty".
  Future<void> _run(Future<void> Function() call) async {
    try {
      await call();
    } on golang.GolangException catch (e) {
      _snack(e.message);
    }
  }

  void _snack(String text) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(text)));
  }

  Future<void> _add() => _run(() async {
    await golang.todo.add(_title.text);
    _title.clear();
  });

  Future<void> _clearDone() => _run(() async {
    final deleted = await golang.todo.clearDone();
    if (deleted > 0) _snack('Deleted $deleted item(s)');
  });

  Future<void> _rename(golang.Item item) async {
    final controller = TextEditingController(text: item.title);
    final title = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Rename'),
        content: TextField(
          controller: controller,
          autofocus: true,
          onSubmitted: (value) => Navigator.pop(context, value),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, controller.text),
            child: const Text('Save'),
          ),
        ],
      ),
    );
    controller.dispose();
    if (title != null) await _run(() => golang.todo.rename(item.id, title));
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        backgroundColor: theme.colorScheme.inversePrimary,
        title: const Text('Todo'),
        actions: [
          IconButton(
            onPressed: _clearDone,
            tooltip: 'Delete done items',
            icon: const Icon(Icons.delete_sweep),
          ),
        ],
      ),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
            child: TextField(
              controller: _title,
              decoration: InputDecoration(
                hintText: 'What needs to be done?',
                border: const OutlineInputBorder(),
                suffixIcon: IconButton(
                  onPressed: _add,
                  tooltip: 'Add',
                  icon: const Icon(Icons.add),
                ),
              ),
              onSubmitted: (_) => _add(),
            ),
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: SegmentedButton<Filter>(
              segments: const [
                ButtonSegment(value: Filter.all, label: Text('All')),
                ButtonSegment(value: Filter.open, label: Text('Open')),
                ButtonSegment(value: Filter.done, label: Text('Done')),
              ],
              selected: {_filter},
              onSelectionChanged: (s) => setState(() => _filter = s.single),
            ),
          ),
          Expanded(
            child: StreamBuilder<List<golang.Item>>(
              stream: _items,
              builder: (context, snapshot) {
                if (snapshot.hasError) {
                  return Center(child: Text('${snapshot.error}'));
                }
                final items = snapshot.data;
                if (items == null) {
                  return const Center(child: CircularProgressIndicator());
                }
                final shown = items.where(
                  (item) => switch (_filter) {
                    Filter.all => true,
                    Filter.open => !item.done,
                    Filter.done => item.done,
                  },
                );
                final open = items.where((item) => !item.done).length;
                return Column(
                  children: [
                    Expanded(
                      child: ListView(
                        children: [
                          for (final item in shown)
                            _ItemTile(
                              key: ValueKey(item.id),
                              item: item,
                              onToggle: () =>
                                  _run(() => golang.todo.toggle(item.id)),
                              onRename: () => _rename(item),
                              onRemove: () =>
                                  _run(() => golang.todo.remove(item.id)),
                            ),
                        ],
                      ),
                    ),
                    Padding(
                      padding: const EdgeInsets.all(8),
                      child: FutureBuilder<String>(
                        future: _version,
                        builder: (context, version) => Text(
                          '$open open · Go todo ${version.data ?? ''}',
                          style: theme.textTheme.bodySmall,
                        ),
                      ),
                    ),
                  ],
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}

class _ItemTile extends StatelessWidget {
  const _ItemTile({
    super.key,
    required this.item,
    required this.onToggle,
    required this.onRename,
    required this.onRemove,
  });

  final golang.Item item;
  final VoidCallback onToggle;
  final VoidCallback onRename;
  final VoidCallback onRemove;

  @override
  Widget build(BuildContext context) {
    final localizations = MaterialLocalizations.of(context);
    final at = (item.doneAt ?? item.created).toLocal();
    return ListTile(
      leading: Checkbox(value: item.done, onChanged: (_) => onToggle()),
      title: Text(
        item.title,
        style: item.done
            ? const TextStyle(decoration: TextDecoration.lineThrough)
            : null,
      ),
      subtitle: Text(
        '${item.done ? 'done' : 'added'} '
        '${localizations.formatShortDate(at)} '
        '${localizations.formatTimeOfDay(TimeOfDay.fromDateTime(at))}',
      ),
      onTap: onRename,
      trailing: IconButton(
        onPressed: onRemove,
        tooltip: 'Delete',
        icon: const Icon(Icons.delete_outline),
      ),
    );
  }
}
