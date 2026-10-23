// The Flutter build hook of the Go library: it builds golang/ with
// "fluffy lib" for the platform and architecture Flutter builds and hands
// the library to Flutter, which bundles it with the app (and on macOS signs
// it). Flutter runs the hook again only when one of the files fluffy lists
// with --deps changed.
//
// Owned by fluffy: fluffy update overwrites this file.
import 'dart:convert';
import 'dart:io';

import 'package:code_assets/code_assets.dart';
import 'package:hooks/hooks.dart';

void main(List<String> args) async {
  await build(args, (input, output) async {
    if (!input.config.buildCodeAssets) return;
    final code = input.config.code;
    final os = code.targetOS;

    // iOS links the Go archive statically into the app. The Xcode build
    // phase fluffy adds to the Runner target builds it, Dart finds the
    // symbols in the process.
    if (os == OS.iOS) {
      output.assets.code.add(
        CodeAsset(
          package: input.packageName,
          name: 'golang',
          linkMode: LookupInProcess(),
        ),
      );
      return;
    }

    final root = input.packageRoot.resolve('..');
    final out = input.outputDirectory;
    final deps = out.resolve('deps.txt');
    final path = _path();
    final fluffy = _find('fluffy', path);

    // The hook can't write lib/, so it only checks that the Dart API was
    // generated from the current Go code.
    await _run(fluffy, ['generate', '--check'], root, path);

    final arch = code.targetArchitecture;
    final cc = code.cCompiler?.compiler;
    await _run(
      fluffy,
      [
        'lib', os.name, // linux, windows, macos, android
        '--arch', arch.name, // arm, arm64, ia32, x64
        '-o', out.toFilePath(),
        '--deps', deps.toFilePath(),
        if (os == OS.android) ...[
          '--android-api',
          '${code.android.targetNdkApi}',
        ],
        if (os == OS.macOS) ...['--min-os', '${code.macOS.targetVersion}'],
        // On Windows the compiler is MSVC, cgo needs MinGW gcc or clang from
        // PATH.
        if (os != OS.windows && cc != null) ...['--cc', cc.toFilePath()],
        // Profile and release builds link, debug builds don't. Flutter strips
        // the library on macOS after writing its dSYM, the Android build when
        // packaging the app.
        if ((os == OS.linux || os == OS.windows) && input.config.linkingEnabled)
          '--strip',
      ],
      root,
      path,
    );

    output.assets.code.add(
      CodeAsset(
        package: input.packageName,
        name: 'golang',
        linkMode: DynamicLoadingBundled(),
        file: os == OS.android
            ? out.resolve('${_androidABIs[arch]}/libgolang.so')
            : out.resolve(os.dylibFileName('golang')),
      ),
    );
    output.dependencies.addAll(
      File.fromUri(deps)
          .readAsLinesSync()
          .where((line) => line.isNotEmpty)
          .map(Uri.file),
    );
  });
}

const _androidABIs = {
  Architecture.arm: 'armeabi-v7a',
  Architecture.arm64: 'arm64-v8a',
  Architecture.ia32: 'x86',
  Architecture.x64: 'x86_64',
};

// Shown when fluffy is missing, which otherwise fails with a bare
// "No such file or directory".
const _notFound =
    r'fluffy was not found on PATH. The Go library of this app is built by the fluffy CLI during the Flutter build. '
    r'Install it with "go install github.com/madzione/fluffy@latest" and make sure its directory '
    r'(usually $(go env GOPATH)/bin, e.g. ~/go/bin) is on PATH, then build again.';

final _separator = Platform.isWindows ? ';' : ':';

// Builds started from the Xcode UI get a minimal PATH without Go or fluffy,
// so the usual install locations are added.
String _path() {
  final env = Platform.environment;
  final home = env['HOME'] ?? env['USERPROFILE'];
  final sep = Platform.pathSeparator;
  return [
    env['PATH'],
    for (final dir in (env['GOPATH'] ?? '').split(_separator))
      if (dir.isNotEmpty) '$dir${sep}bin',
    if (home != null) '$home${sep}go${sep}bin',
    if (Platform.isWindows)
      r'C:\Program Files\Go\bin'
    else ...[
      '/usr/local/go/bin',
      '/opt/homebrew/bin',
      '/usr/local/bin',
    ],
  ].whereType<String>().where((dir) => dir.isNotEmpty).join(_separator);
}

String _find(String name, String path) {
  final file = Platform.isWindows ? '$name.exe' : name;
  for (final dir in path.split(_separator)) {
    final candidate = File('$dir${Platform.pathSeparator}$file');
    if (candidate.existsSync()) return candidate.path;
  }
  _fail(_notFound);
}

Future<void> _run(
  String executable,
  List<String> args,
  Uri dir,
  String path,
) async {
  final result = await Process.run(
    executable,
    args,
    workingDirectory: dir.toFilePath(),
    environment: {'PATH': path},
  );
  if (result.exitCode != 0) {
    _fail('fluffy ${args.first} failed:\n${result.stdout}${result.stderr}');
  }
}

// Fails the build with message. Every line starts with "error:": without -v
// the Linux and Windows builds only show such lines, and Xcode lists them as
// issues.
Never _fail(String message) {
  stderr.writeln(
    const LineSplitter()
        .convert(message.trim())
        .map((line) => 'error: $line')
        .join('\n'),
  );
  exit(1);
}
