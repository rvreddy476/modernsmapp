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
    // If the server returns a new CSRF token in headers, capture it.
    //
    // Important: we MUST use the list accessor `headers['set-cookie']`,
    // not `headers.value('set-cookie')`. Dio's `value()` throws
    // `Exception: "set-cookie" header has more than one value` the
    // moment the server returns more than one cookie (the auth
    // endpoints set access_token + refresh_token + session in one
    // response, so this hit on every login/register).
    String? serverToken = response.headers.value('X-CSRF-Token');
    if (serverToken == null) {
      final cookies = response.headers['set-cookie'] ?? const <String>[];
      for (final raw in cookies) {
        // Cookie format: "csrf_token=abc; Path=/; HttpOnly". Split on
        // ';' to isolate the name=value pair, then on '=' for value.
        for (final part in raw.split(';')) {
          final kv = part.trim();
          if (kv.startsWith('csrf_token=')) {
            serverToken = kv.substring('csrf_token='.length);
            break;
          }
        }
        if (serverToken != null) break;
      }
    }

    if (serverToken != null && serverToken.isNotEmpty) {
      _csrfToken = serverToken;
    }

    handler.next(response);
  }
}
