// Pulse string resolver.
//
// Lightweight legacy approach — no flutter_localizations dependency. Pick the
// active locale via `AppStrings.setLocale('hi')` (see plan C9 for migration to
// flutter_localizations + ARB once we have translations to load).
//
// Usage:
// ```dart
// import 'package:atpost_app/l10n/app_strings.dart';
// Text(AppStrings.t('pulse.intent.title'));
// ```

import 'package:atpost_app/l10n/app_strings_bn.dart';
import 'package:atpost_app/l10n/app_strings_en.dart';
import 'package:atpost_app/l10n/app_strings_hi.dart';
import 'package:atpost_app/l10n/app_strings_mr.dart';
import 'package:atpost_app/l10n/app_strings_ta.dart';
import 'package:atpost_app/l10n/app_strings_te.dart';

class AppStrings {
  AppStrings._();

  /// Currently selected locale tag (`'en'`, `'hi'`, `'ta'`, `'te'`, `'bn'`,
  /// `'mr'`). Defaults to English. Update from app boot once we wire a
  /// language picker.
  static String _locale = 'en';

  // Note: `final` (not const) so language bundles can spread `appStringsEn`
  // and add their own overrides on top.
  static final Map<String, Map<String, String>> _bundles = {
    'en': appStringsEn,
    'hi': appStringsHi,
    'ta': appStringsTa,
    'te': appStringsTe,
    'bn': appStringsBn,
    'mr': appStringsMr,
  };

  static String get locale => _locale;

  static void setLocale(String tag) {
    if (_bundles.containsKey(tag)) {
      _locale = tag;
    }
  }

  /// Look up a key. Falls back to English if missing in the active locale,
  /// then to the key itself if missing entirely (so missing strings are
  /// visible in the UI rather than blank).
  static String t(String key) {
    final active = _bundles[_locale] ?? appStringsEn;
    return active[key] ?? appStringsEn[key] ?? key;
  }
}
