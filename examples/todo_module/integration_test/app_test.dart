import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import 'package:todo_module/main.dart';

// Runs the app against the real Go service. On desktop it keeps the list in
// the user's config directory, so point XDG_CONFIG_HOME at an empty one:
//
//   XDG_CONFIG_HOME=$(mktemp -d) flutter test integration_test -d linux
void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('the Go service keeps the list', (tester) async {
    await tester.pumpWidget(const TodoApp());
    await tester.pumpAndSettle();
    expect(find.textContaining('Go todo 1.0.0'), findsOneWidget);

    await tester.enterText(find.byType(TextField), '   ');
    await tester.tap(find.byTooltip('Add'));
    await tester.pumpAndSettle();
    expect(find.text('the title is empty'), findsOneWidget);

    await tester.enterText(find.byType(TextField), 'Integration');
    await tester.tap(find.byTooltip('Add'));
    await tester.pumpAndSettle();
    final tile = find.ancestor(
      of: find.text('Integration'),
      matching: find.byType(ListTile),
    );
    expect(tile, findsOneWidget);

    await tester.tap(
      find.descendant(of: tile, matching: find.byType(Checkbox)),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byTooltip('Delete done items'));
    await tester.pumpAndSettle();
    expect(find.textContaining('done item(s)?'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Delete'));
    await tester.pumpAndSettle();
    expect(find.text('Integration'), findsNothing);
    expect(find.textContaining('Deleted'), findsOneWidget);
  });
}
