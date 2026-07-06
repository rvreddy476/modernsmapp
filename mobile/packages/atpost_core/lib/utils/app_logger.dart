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
    final timestamp = DateTime.now().toIso8601String();
    final logTag = tag != null ? '[$tag]' : '';
    final fullMessage = '$timestamp [$level]$logTag $message';

    dev.log(
      fullMessage,
      name: 'atpost.app',
      error: error,
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
          if (error != null) debugPrint('   ↳ cause: $error');
          if (stackTrace != null) debugPrint('   ↳ stack: $stackTrace');
        case 'WARN':
          debugPrint('⚠️  $fullMessage');
          if (error != null) debugPrint('   ↳ cause: $error');
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
