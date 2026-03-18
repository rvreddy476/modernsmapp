import 'package:atpost_app/core/errors/app_exception.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:dio/dio.dart';

/// Centralized error handler that converts raw exceptions to typed [AppException]
/// and logs them via [AppLogger].
class ErrorHandler {
  const ErrorHandler._();

  static const _tag = 'ErrorHandler';

  /// Converts any error to a typed [AppException], logs it, and returns it.
  ///
  /// Use [context] to provide additional info about where the error occurred
  /// (e.g. 'FeedRepository.getHomeFeed').
  static AppException handle(Object error, StackTrace stackTrace, {String? context}) {
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

  /// Converts a raw error to the appropriate [AppException] subtype.
  static AppException _convert(Object error, StackTrace stackTrace) {
    if (error is AppException) return error;

    if (error is DioException) {
      return _fromDioException(error, stackTrace);
    }

    return ServerException(
      message: error.toString(),
      originalError: error,
      stackTrace: stackTrace,
    );
  }

  /// Maps [DioException] to typed [AppException] based on status code and type.
  static AppException _fromDioException(DioException error, StackTrace stackTrace) {
    // Network-level errors (no response from server)
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

  /// Extracts a human-readable message from the server response.
  static String _extractServerMessage(DioException error) {
    final data = error.response?.data;
    if (data is Map<String, dynamic>) {
      final message = data['message'] ?? data['error'] ?? data['detail'];
      if (message is String && message.isNotEmpty) return message;
    }
    return error.message ?? 'An unexpected error occurred';
  }

  /// Extracts per-field validation errors from server response.
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
