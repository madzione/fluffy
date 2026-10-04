import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:todo_module/golang/golang.dart';
import 'package:todo_module/main.dart';

/// Plays the Go todo service on a fake bridge. Bound methods get their
/// arguments as a list.
class FakeTodo {
  FakeTodo(this.go) {
    go.receiver.handle('todo.Version', (_) => '1.0.0');
    go.receiver.stream('todo.Watch', (_) async* {
      yield List.of(items);
      await for (final _ in _changes.stream) {
        yield List.of(items);
      }
    });
    go.receiver.handle('todo.Add', (data) {
      final [title as String] = data as List<dynamic>;
      if (title.trim().isEmpty) throw 'the title is empty';
      final item = {
        'id': items.length + 1,
        'title': title.trim(),
        'done': false,
        'created': DateTime(2026, 10, 4, 12).toUtc().toIso8601String(),
      };
      _change(() => items.add(item));
      return item;
    });
    go.receiver.handle('todo.Toggle', (data) {
      final [id as int] = data as List<dynamic>;
      final item = items.firstWhere((item) => item['id'] == id);
      _change(() => item['done'] = !(item['done'] as bool));
      return item;
    });
    // Like the Go service, ask Dart through todo.UI before deleting.
    go.receiver.handle('todo.ClearDone', (_) async {
      final done = items.where((item) => item['done'] == true).length;
      final ok = await go.emitter.request<bool>('todo.UI.Confirm', [
        'Delete $done done item(s)?',
      ]);
      if (!ok) return 0;
      _change(() => items.removeWhere((item) => item['done'] == true));
      return done;
    });
  }

  final Bridge go;
  final items = <Map<String, Object?>>[];
  final _changes = StreamController<void>.broadcast();

  void _change(void Function() fn) {
    fn();
    _changes.add(null);
  }
}

void main() {
  testWidgets('adds, completes and clears items through Go', (tester) async {
    final todo = FakeTodo(Bridge.fake());

    await tester.pumpWidget(const TodoApp());
    await tester.pumpAndSettle();
    expect(find.text('0 open · Go todo 1.0.0'), findsOneWidget);

    await tester.enterText(find.byType(TextField), '  ');
    await tester.tap(find.byTooltip('Add'));
    await tester.pumpAndSettle();
    expect(find.text('the title is empty'), findsOneWidget);

    await tester.enterText(find.byType(TextField), 'Milk');
    await tester.testTextInput.receiveAction(TextInputAction.done);
    await tester.pumpAndSettle();
    expect(find.text('Milk'), findsOneWidget);
    expect(find.text('1 open · Go todo 1.0.0'), findsOneWidget);

    await tester.tap(find.byType(Checkbox));
    await tester.pumpAndSettle();
    expect(todo.items.single['done'], isTrue);

    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();
    expect(find.text('Milk'), findsNothing);
    await tester.tap(find.text('All'));
    await tester.pumpAndSettle();

    // Cancelling the question Go asks keeps the item.
    await tester.tap(find.byTooltip('Delete done items'));
    await tester.pumpAndSettle();
    expect(find.text('Delete 1 done item(s)?'), findsOneWidget);
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(find.text('Milk'), findsOneWidget);

    await tester.tap(find.byTooltip('Delete done items'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Delete'));
    await tester.pumpAndSettle();
    expect(find.text('Milk'), findsNothing);
    expect(find.text('Deleted 1 item(s)'), findsOneWidget);
  });
}
