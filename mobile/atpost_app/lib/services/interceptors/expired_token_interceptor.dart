import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:dio/dio.dart';

/// Interceptor to handle expired tokens by refreshing and retrying the request.
/// Extends QueuedInterceptor to prevent multiple refresh calls.
class ExpiredTokenInterceptor extends QueuedInterceptor {
  final AuthService _auth;
  final Dio _dio;
  static const _tag = 'ExpiredTokenInterceptor';

  ExpiredTokenInterceptor(this._auth, this._dio);

  @override
  void onError(DioException err, ErrorInterceptorHandler handler) async {
    // Catch 401 Unauthorized errors
    if (err.response?.statusCode == 401) {
      AppLogger.warn('Unauthorized request (401) detected at: ${err.requestOptions.path}', tag: _tag);

      final options = err.requestOptions;

      // Attempt to refresh the token
      final success = await _auth.refreshAccessToken();

      if (success) {
        AppLogger.info('Retrying original request with new token: ${options.path}', tag: _tag);

        try {
          // Update the headers with the new token
          final token = _auth.token;
          if (token != null) {
            options.headers['Authorization'] = 'Bearer $token';
          }

          // Re-issue the original request
          final response = await _dio.fetch(options);
          return handler.resolve(response);
        } catch (e) {
          AppLogger.error('Failed to retry request after refresh', tag: _tag, error: e);
          return handler.next(err);
        }
      } else {
        AppLogger.error('Token refresh failed. Logging out user.', tag: _tag);
        _auth.logout();
      }
    }

    // Pass the error to the next interceptor/handler
    handler.next(err);
  }
}
