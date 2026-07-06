import 'package:atpost_network/auth_session.dart';
import 'package:dio/dio.dart';

/// Interceptor to inject authentication tokens and user context into every request.
class AuthInterceptor extends Interceptor {
  final AuthSession _auth;

  AuthInterceptor(this._auth);

  @override
  void onRequest(RequestOptions options, RequestInterceptorHandler handler) {
    final token = _auth.token;
    final userId = _auth.userId;

    if (token != null) {
      options.headers['Authorization'] = 'Bearer $token';
    }

    if (userId != null) {
      options.headers['X-User-Id'] = userId;
    }

    // Standard headers for all requests
    options.headers['X-Requested-With'] = 'XMLHttpRequest';

    handler.next(options);
  }
}
