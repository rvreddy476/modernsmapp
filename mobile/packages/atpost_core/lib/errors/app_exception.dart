import 'package:dio/dio.dart';

/// Base exception type for all app-level errors.
/// Provides both technical details and user-facing messages.
sealed class AppException implements Exception {
  const AppException({
    required this.message,
    this.statusCode,
    this.originalError,
    this.stackTrace,
  });

  /// Technical error message for logging.
  final String message;

  /// HTTP status code, if applicable.
  final int? statusCode;

  /// The original error that caused this exception.
  final Object? originalError;

  /// Stack trace from the original error.
  final StackTrace? stackTrace;

  /// Human-friendly message suitable for SnackBars / UI display.
  String get userMessage;

  @override
  String toString() => '$runtimeType($statusCode): $message';
}

/// Network-level errors: timeout, no connection, DNS failure.
class NetworkException extends AppException {
  const NetworkException({
    required super.message,
    super.statusCode,
    super.originalError,
    super.stackTrace,
  });

  @override
  String get userMessage => 'No internet connection. Please check your network and try again.';

  factory NetworkException.fromDioException(DioException e, {StackTrace? st}) {
    final String msg;
    switch (e.type) {
      case DioExceptionType.connectionTimeout:
        msg = 'Connection timed out';
      case DioExceptionType.sendTimeout:
        msg = 'Request send timed out';
      case DioExceptionType.receiveTimeout:
        msg = 'Response timed out';
      case DioExceptionType.connectionError:
        msg = 'Could not connect to server';
      default:
        msg = e.message ?? 'Network error';
    }
    return NetworkException(
      message: msg,
      originalError: e,
      stackTrace: st ?? e.stackTrace,
    );
  }
}

/// Server-side errors: 500, 502, 503, etc.
class ServerException extends AppException {
  const ServerException({
    required super.message,
    super.statusCode,
    super.originalError,
    super.stackTrace,
  });

  @override
  String get userMessage => 'Something went wrong on our end. Please try again later.';
}

/// Authentication/authorization errors: 401, 403.
class AuthException extends AppException {
  const AuthException({
    required super.message,
    super.statusCode,
    super.originalError,
    super.stackTrace,
  });

  @override
  String get userMessage => statusCode == 403
      ? 'You don\'t have permission to perform this action.'
      : 'Your session has expired. Please sign in again.';
}

/// Resource not found: 404.
class NotFoundException extends AppException {
  const NotFoundException({
    required super.message,
    super.statusCode = 404,
    super.originalError,
    super.stackTrace,
  });

  @override
  String get userMessage => 'The requested content was not found.';
}

/// Validation / bad request: 400, 422.
class ValidationException extends AppException {
  const ValidationException({
    required super.message,
    super.statusCode,
    super.originalError,
    super.stackTrace,
    this.fieldErrors = const {},
  });

  /// Per-field validation errors from the server, if available.
  final Map<String, String> fieldErrors;

  @override
  String get userMessage => fieldErrors.isNotEmpty
      ? fieldErrors.values.first
      : 'Please check your input and try again.';
}

/// Cache-related errors.
class CacheException extends AppException {
  const CacheException({
    required super.message,
    super.originalError,
    super.stackTrace,
  });

  @override
  String get userMessage => 'Could not load cached data.';
}
