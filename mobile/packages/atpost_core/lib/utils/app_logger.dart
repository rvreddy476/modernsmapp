import 'dart:developer' as dev;
import 'package:flutter/foundation.dart';

/// A production-grade logger that wraps dart:developer log for structured output.
class AppLogger {
  const AppLogger._();

  static void info(String message, {String? tag, Object? error, StackTrace? stackTrace}) {
    _log('INFO', message, tag: tag, error: error, stackTrace: stackTrace);
  }

  static void warn(String message, {String? tag, Object? error, StackTrace? stackTrace}) {
    _log('WARN', message, tag: tag, error: error, stackTrace: stackTrace);
  }

  static void error(String message, {String? tag, Object? error, StackTrace? stackTrace}) {
    _log('ERROR', message, tag: tag, error: error, stackTrace: stackTrace);
  }

  static void debug(String message, {String? tag}) {
    if (kDebugMode) {
      _log('DEBUG', message, tag: tag);
    }
  }

  static void _log(
    String level,
    String message, {
    String? tag,
    Object? error,
    StackTrace? stackTrace,
  }) {
    // SECURITY: Sanitize sensitive information from logs in all builds.
    // Prevents accidental leak of JWTs, passwords, or PII to system logs.
    final sanitizedMessage = _sanitize(message);
    final sanitizedError = error != null ? _sanitize(error.toString()) : null;

    final timestamp = DateTime.now().toIso8601String();
    final logTag = tag != null ? '[$tag]' : '';
    final fullMessage = '$timestamp [$level]$logTag $sanitizedMessage';

    dev.log(
      fullMessage,
      name: 'atpost.app',
      error: sanitizedError,
      stackTrace: stackTrace,
      level: _levelToInt(level),
    );

    // In debug builds, also surface every level via debugPrint so the
    // line shows up in `adb logcat` (the I/flutter channel). dev.log
    // alone only reaches the attached Dart VM service. ERROR keeps the
    // 🚨 marker; the rest get a compact prefix so they're grep-able.
    if (kDebugMode) {
      switch (level) {
        case 'ERROR':
          debugPrint('🚨 CRITICAL ERROR: $fullMessage');
          if (sanitizedError != null) debugPrint('   ↳ cause: $sanitizedError');
          if (stackTrace != null) debugPrint('   ↳ stack: $stackTrace');
        case 'WARN':
          debugPrint('⚠️  $fullMessage');
          if (sanitizedError != null) debugPrint('   ↳ cause: $sanitizedError');
        case 'INFO':
          debugPrint('ℹ️  $fullMessage');
        case 'DEBUG':
          debugPrint('·  $fullMessage');
      }
    } else if (level == 'ERROR') {
      // In a real production app, you might send this to Sentry/Firebase Crashlytics here.
      debugPrint('🚨 CRITICAL ERROR: $fullMessage');
    }
  }

  /// Removes sensitive patterns (JWTs, auth headers, etc.) from log strings.
  static String _sanitize(String input) {
    var output = input;
    // Mask JWT-like structures (header.payload.signature)
    output = output.replaceAll(RegExp(r'eyJ[A-Za-z0-9-_=]+\.[A-Za-z0-9-_=]+\.?[A-Za-z0-9-_.+/=]*'), '[REDACTED_JWT]');
    // Mask common Auth/Bearer patterns. NOTE: must use replaceAllMapped —
    // String.replaceAll inserts the replacement literally and does NOT expand
    // $1/$2 backreferences, so the old code emitted a literal "$1$2 [REDACTED]".
    output = output.replaceAllMapped(
      RegExp(r'(Authorization|Bearer|token|password|secret|key)(=|:)\s*[^\s,]+',
          caseSensitive: false),
      (m) => '${m.group(1)}${m.group(2)} [REDACTED]',
    );
    // Mask potential email PII
    output = output.replaceAll(RegExp(r'[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}', caseSensitive: false), '[PII_EMAIL]');
    return output;
  }

  static int _levelToInt(String level) {
    switch (level) {
      case 'DEBUG': return 500;
      case 'INFO': return 800;
      case 'WARN': return 900;
      case 'ERROR': return 1000;
      default: return 0;
    }
  }
}
