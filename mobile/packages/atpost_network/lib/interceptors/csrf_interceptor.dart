import 'package:dio/dio.dart';

/// Interceptor to handle CSRF protection.
///
/// Strictly server-driven: it captures tokens from 'set-cookie' or 'X-CSRF-Token'
/// response headers and injects them into subsequent mutating requests.
/// Client-side generation of CSRF tokens is insecure and prohibited.
class CsrfInterceptor extends Interceptor {
  String? _csrfToken;

  @override
  void onRequest(RequestOptions options, RequestInterceptorHandler handler) {
    // Inject CSRF token for mutating methods if available.
    // If null, the request proceeds—highly secured backends will reject the
    // mutation, prompting the client to perform a GET 'handshake' first.
    final method = options.method.toUpperCase();
    if (['POST', 'PUT', 'DELETE', 'PATCH'].contains(method)) {
      if (_csrfToken != null) {
        options.headers['X-CSRF-Token'] = _csrfToken;
      }
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
