import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import 'package:roundtrip/checks.dart';

// Runs every round trip check against the real Go library and fails with
// the list of failed checks. On desktop:
//
//   fvm flutter test integration_test -d linux
//
// On the web, after "fluffy lib web", with chromedriver --port=4444 running
// (add --release, or --release --wasm for WebAssembly):
//
//   fvm flutter drive --driver=test_driver/integration_test.dart \
//     --target=integration_test/app_test.dart -d web-server --browser-name=chrome
void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('round trip checks', (tester) async {
    registerHandlers();
    final lines = <String>[], failed = <String>[];
    for (final check in checks) {
      final result = await tester.runAsync(() => runCheck(check));
      final line =
          '${result!.passed ? 'ok  ' : 'FAIL'} ${check.group} / ${check.name}'
          '${result.detail != null ? ' (${result.detail})' : ''}';
      // ignore: avoid_print
      print(line);
      lines.add(line);
      if (!result.passed) failed.add('$line\n    ${result.error}');
    }
    // flutter drive writes this to build/integration_response_data.json.
    binding.reportData = {'platform': platform, 'checks': lines};
    expect(
      failed,
      isEmpty,
      reason: '${failed.length} checks failed on $platform',
    );
  });
}
