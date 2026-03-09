import 'dart:math';
import 'package:dio/dio.dart';

/// Interceptor to handle CSRF protection.
/// Mirrors the web app's requirement for X-CSRF-Token on mutating requests.
class CsrfInterceptor extends Interceptor {
  String? _csrfToken;

  /// Generates a simple token if one isn't available.
  /// In a production environment, this might be fetched from a 'priming' endpoint
  /// or extracted from a response cookie.
  String _ensureToken() {
    if (_csrfToken == null) {
      final random = Random.secure();
      final values = List<int>.generate(16, (i) => random.nextInt(256));
      _csrfToken = values.map((b) => b.toRadixString(16).padLeft(2, '0')).join();
    }
    return _csrfToken!;
  }

  @override
  void onRequest(RequestOptions options, RequestInterceptorHandler handler) {
    // Inject CSRF token for mutating methods as required by the backend
    final method = options.method.toUpperCase();
    if (['POST', 'PUT', 'DELETE', 'PATCH'].contains(method)) {
      options.headers['X-CSRF-Token'] = _ensureToken();
    }

    handler.next(options);
  }

  @override
  void onResponse(Response response, ResponseInterceptorHandler handler) {
    // If the server returns a new CSRF token in headers, capture it
    final serverToken = response.headers.value('X-CSRF-Token') ??
                       response.headers.value('set-cookie')?.split(';')
                       .firstWhere((c) => c.trim().startsWith('csrf_token='), orElse: () => '')
                       .split('=')
                       .last;

    if (serverToken != null && serverToken.isNotEmpty) {
      _csrfToken = serverToken;
    }

    handler.next(response);
  }
}
