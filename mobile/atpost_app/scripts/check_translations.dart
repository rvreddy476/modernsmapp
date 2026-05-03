// scripts/check_translations.dart
//
// Sprint 5 — translation coverage checker.
//
// Lists every key in the English bundle that is NOT overridden in each
// non-English bundle. Output is a CSV-ish table that the translator team
// can paste into a Google Sheet and assign rows.
//
// Usage:
//   dart run scripts/check_translations.dart
//
// (Run from the repo root. Doesn't depend on the Flutter SDK — pure Dart.)
//
// Implementation note: we don't import the bundle Dart files directly here
// because that would couple the script to Flutter's package resolution.
// Instead we parse the simple `'key': 'value'` lines — every bundle file
// follows the same shape and the `'key'` is always single-quoted in source.

import 'dart:io';

const List<String> _languageFiles = [
  'lib/l10n/app_strings_hi.dart',
  'lib/l10n/app_strings_ta.dart',
  'lib/l10n/app_strings_te.dart',
  'lib/l10n/app_strings_bn.dart',
  'lib/l10n/app_strings_mr.dart',
];

const String _englishFile = 'lib/l10n/app_strings_en.dart';

void main(List<String> args) {
  final enKeys = _extractKeys(_englishFile);
  if (enKeys.isEmpty) {
    stderr.writeln('No keys found in $_englishFile — is the path right?');
    exit(2);
  }

  final report = StringBuffer()
    ..writeln('language,covered,missing,total,coverage_pct,missing_keys');

  for (final path in _languageFiles) {
    final keys = _extractKeys(path);
    final missing = enKeys.where((k) => !keys.contains(k)).toList()..sort();
    final covered = enKeys.length - missing.length;
    final pct = (covered / enKeys.length * 100).toStringAsFixed(1);
    final lang = path.split('_').last.replaceAll('.dart', '');
    report.writeln(
      '$lang,$covered,${missing.length},${enKeys.length},$pct%,'
      '"${missing.join("|")}"',
    );
  }

  stdout.writeln(report.toString());
}

/// Returns the set of `'<key>'` literals declared in a bundle file. Keys
/// follow the convention `'pulse.<area>.<element>'` so the regex is tight.
Set<String> _extractKeys(String path) {
  final f = File(path);
  if (!f.existsSync()) return const {};
  final src = f.readAsStringSync();
  final re = RegExp(r"'(pulse\.[A-Za-z0-9_.]+)':");
  final keys = <String>{};
  for (final m in re.allMatches(src)) {
    keys.add(m.group(1)!);
  }
  return keys;
}
