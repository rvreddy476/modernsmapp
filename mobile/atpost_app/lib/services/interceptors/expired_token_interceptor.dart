import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:dio/dio.dart';

/// Interceptor to handle expired tokens by refreshing and retrying the request.
/// Extends QueuedInterceptor to prevent multiple refresh calls.
class ExpiredTokenInterceptor extends QueuedInterceptor {
  static const _retryMarkerKey = 'expired_token_retry';
  final AuthService _auth;
  final Dio _dio;
  static const _tag = 'ExpiredTokenInterceptor';

  ExpiredTokenInterceptor(this._auth, this._dio);

  @override
  void onError(DioException err, ErrorInterceptorHandler handler) async {
    // Catch 401 Unauthorized errors
    if (err.response?.statusCode == 401) {
      final originalOptions = err.requestOptions;
      final hasRetried = originalOptions.extra[_retryMarkerKey] == true;
      AppLogger.warn(
        'Unauthorized request (401) detected at: ${originalOptions.path}',
        tag: _tag,
      );

      if (hasRetried) {
        AppLogger.warn(
          'Request remained unauthorized after token refresh; skipping retry',
          tag: _tag,
        );
        handler.next(err);
        return;
      }

      final refreshedOptions = originalOptions.copyWith(
        headers: Map<String, dynamic>.from(originalOptions.headers),
        extra: <String, dynamic>{
          ...originalOptions.extra,
          _retryMarkerKey: true,
        },
      );

      // Attempt to refresh the token
      final success = await _auth.refreshAccessToken();

      if (success) {
        AppLogger.info(
          'Retrying original request with new token: ${refreshedOptions.path}',
          tag: _tag,
        );

        try {
          // Update the headers with the new token
          final token = _auth.token;
          if (token != null && token.isNotEmpty) {
            refreshedOptions.headers['Authorization'] = 'Bearer $token';
          }

          // Re-issue the original request
          final response = await _dio.fetch(refreshedOptions);
          return handler.resolve(response);
        } on DioException catch (retryError) {
          AppLogger.error(
            'Failed to retry request after refresh',
            tag: _tag,
            error: retryError,
          );
          return handler.next(retryError);
        } catch (e) {
          AppLogger.error(
            'Failed to retry request after refresh',
            tag: _tag,
            error: e,
          );
          return handler.next(err);
        }
      } else {
        AppLogger.error('Token refresh failed. Logging out user.', tag: _tag);
        _auth.logout();
        handler.next(err);
        return;
      }
    }

    // Pass the error to the next interceptor/handler
    handler.next(err);
  }
}
