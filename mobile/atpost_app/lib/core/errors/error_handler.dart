import 'dart:async';
import 'dart:io';

import 'package:atpost_app/core/errors/app_exception.dart';
import 'package:atpost_app/core/errors/user_messages.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:dio/dio.dart';

/// Centralized error handler that converts raw exceptions to typed [AppException]
/// and logs them via [AppLogger]. Includes resilience features for scale.
class ErrorHandler {
  const ErrorHandler._();

  static const _tag = 'ErrorHandler';

  /// Converts any error to a typed [AppException], logs it, and returns it.
  static AppException handle(
    Object error,
    StackTrace stackTrace, {
    String? context,
  }) {
    final appException = _convert(error, stackTrace);

    final ctx = context != null ? ' [$context]' : '';
    AppLogger.error(
      '${appException.runtimeType}$ctx: ${appException.message}',
      tag: _tag,
      error: appException.originalError ?? error,
      stackTrace: stackTrace,
    );

    return appException;
  }

  /// Executes a [task] with automatic exponential backoff retry logic.
  /// Essential for handling billions of users with varying network quality.
  static Future<T> retry<T>(
    Future<T> Function() task, {
    int maxAttempts = 3,
    Duration initialDelay = const Duration(milliseconds: 500),
  }) async {
    int attempts = 0;
    while (true) {
      attempts++;
      try {
        return await task();
      } catch (e, st) {
        final exception = _convert(e, st);
        final isRetryable = _isRetryable(exception);

        if (attempts >= maxAttempts || !isRetryable) {
          throw exception;
        }

        final delay =
            initialDelay * (attempts * attempts); // Exponential backoff
        AppLogger.warn(
          'Task failed (attempt $attempts/$maxAttempts). Retrying in ${delay.inMilliseconds}ms...',
          tag: _tag,
          error: e,
        );
        await Future.delayed(delay);
      }
    }
  }

  /// Determines if an exception is worth retrying (e.g., transient network issues).
  static bool _isRetryable(AppException e) {
    if (e is NetworkException) return true;
    if (e is ServerException) {
      // Retry on 502 Bad Gateway, 503 Service Unavailable, 504 Gateway Timeout
      return e.statusCode == 502 || e.statusCode == 503 || e.statusCode == 504;
    }
    return false;
  }

  /// Converts a raw error to the appropriate [AppException] subtype.
  static AppException _convert(Object error, StackTrace stackTrace) {
    if (error is AppException) return error;

    if (error is DioException) {
      return _fromDioException(error, stackTrace);
    }

    if (error is SocketException) {
      return NetworkException(
        message: 'No internet connection',
        originalError: error,
        stackTrace: stackTrace,
      );
    }

    if (error is TimeoutException) {
      return NetworkException(
        message: 'Request timed out',
        originalError: error,
        stackTrace: stackTrace,
      );
    }

    return ServerException(
      message: error.toString(),
      originalError: error,
      stackTrace: stackTrace,
    );
  }

  /// Maps [DioException] to typed [AppException] based on status code and type.
  static AppException _fromDioException(
    DioException error,
    StackTrace stackTrace,
  ) {
    if (error.type == DioExceptionType.connectionTimeout ||
        error.type == DioExceptionType.sendTimeout ||
        error.type == DioExceptionType.receiveTimeout ||
        error.type == DioExceptionType.connectionError) {
      return NetworkException.fromDioException(error, st: stackTrace);
    }

    final statusCode = error.response?.statusCode;
    final serverMessage = _extractServerMessage(error);

    return switch (statusCode) {
      400 || 422 => ValidationException(
        message: serverMessage,
        statusCode: statusCode,
        originalError: error,
        stackTrace: stackTrace,
        fieldErrors: _extractFieldErrors(error),
      ),
      401 || 403 => AuthException(
        message: serverMessage,
        statusCode: statusCode,
        originalError: error,
        stackTrace: stackTrace,
      ),
      // 409 previously had no case and fell through to the `_` arm, so
      // "this email is already registered" — an ordinary, expected outcome —
      // was reported to the user as a NETWORK failure. It is a validation
      // conflict: the request was understood and refused for a reason the
      // user can act on.
      409 => ValidationException(
        message: serverMessage,
        statusCode: statusCode,
        originalError: error,
        stackTrace: stackTrace,
        fieldErrors: _extractFieldErrors(error),
      ),
      404 => NotFoundException(
        message: serverMessage,
        statusCode: statusCode,
        originalError: error,
        stackTrace: stackTrace,
      ),
      final code? when code >= 500 => ServerException(
        message: serverMessage,
        statusCode: code,
        originalError: error,
        stackTrace: stackTrace,
      ),
      _ => NetworkException(
        message: serverMessage,
        statusCode: statusCode,
        originalError: error,
        stackTrace: stackTrace,
      ),
    };
  }

  /// The backend's machine-readable error code, e.g. `CONSENT_REQUIRED`.
  ///
  /// This is the field that lets the UI say something specific. It was being
  /// discarded entirely, which is why every failure looked the same.
  static String? extractServerCode(DioException error) {
    final data = error.response?.data;
    if (data is Map<String, dynamic>) {
      final nested = data['error'];
      if (nested is Map<String, dynamic>) {
        final code = nested['code'];
        if (code is String && code.isNotEmpty) return code;
      }
      final topLevel = data['code'];
      if (topLevel is String && topLevel.isNotEmpty) return topLevel;
    }
    return null;
  }

  /// The sentence to actually show a person, for any Dio failure.
  static String userMessageFor(DioException error) => UserMessages.resolve(
        code: extractServerCode(error),
        statusCode: error.response?.statusCode,
        serverMessage: _extractServerMessage(error),
      );

  static String _extractServerMessage(DioException error) {
    final data = error.response?.data;
    if (data is Map<String, dynamic>) {
      final topLevelMessage = data['message'] ?? data['detail'];
      if (topLevelMessage is String && topLevelMessage.isNotEmpty) {
        return topLevelMessage;
      }

      final nestedError = data['error'];
      if (nestedError is Map<String, dynamic>) {
        final nestedMessage = nestedError['message'] ?? nestedError['detail'];
        if (nestedMessage is String && nestedMessage.isNotEmpty) {
          return nestedMessage;
        }
      }

      if (nestedError is String && nestedError.isNotEmpty) {
        return nestedError;
      }
    }
    return error.message ?? 'An unexpected error occurred';
  }

  static Map<String, String> _extractFieldErrors(DioException error) {
    final data = error.response?.data;
    if (data is Map<String, dynamic>) {
      final errors = data['errors'] ?? data['field_errors'];
      if (errors is Map<String, dynamic>) {
        return errors.map((key, value) => MapEntry(key, value.toString()));
      }
    }
    return const {};
  }
}
