import 'dart:io' show Platform;
import 'package:flutter/foundation.dart' show kIsWeb;

/// Environment configuration for API endpoints.
///
/// Set [externalDomain] to route all traffic through Caddy reverse proxy
/// (e.g. "cleestudio.com") for external/HTTPS testing. When null, uses
/// direct localhost/emulator connections.
class Environment {
  const Environment._();

  /// Set to a domain (e.g. "cleestudio.com") to use external HTTPS endpoints.
  /// Leave null for local development with direct service ports.
  static String? externalDomain = 'cleestudio.com';

  // Resolve host: Android emulator uses 10.0.2.2, everything else uses localhost
  static String get _host {
    if (kIsWeb) return 'localhost';
    try {
      if (Platform.isAndroid) return '10.0.2.2';
    } catch (_) {}
    return 'localhost';
  }

  // Base URLs — auto-detect platform, or use external domain if set
  static String get apiBaseUrl {
    if (externalDomain != null) return 'https://$externalDomain';
    return 'http://$_host:8080';
  }

  static Uri get wsGatewayUri {
    if (externalDomain != null) {
      return Uri(
        scheme: 'wss',
        host: externalDomain!,
        path: '/v1/ws/connect',
      );
    }
    return Uri(
      scheme: 'ws',
      host: _host,
      port: 8093,
      path: '/v1/ws/connect',
    );
  }

  static Uri buildWsGatewayUri([Map<String, String>? queryParameters]) {
    final uri = wsGatewayUri;
    if (queryParameters == null || queryParameters.isEmpty) {
      return uri;
    }
    return uri.replace(
      queryParameters: <String, String>{
        ...uri.queryParameters,
        ...queryParameters,
      },
    );
  }

  static String get wsBaseUrl => wsGatewayUri.toString();

  static String get wsGatewayUrl => wsGatewayUri.toString();

  // API paths
  static const String authPath = '/v1/auth';
  static const String usersPath = '/v1/users';
  static const String profilesPath = '/v1/profiles';
  static const String postsPath = '/v1/posts';
  static const String feedPath = '/v1/feed';
  static const String mediaPath = '/v1/media';
  static const String notificationsPath = '/v1/notifications';
  static const String chatPath = '/v1/chat';
  static const String graphPath = '/v1/graph';
  static const String searchPath = '/v1/search';
  static const String suggestionsPath = '/v1/suggestions';
  static const String analyticsPath = '/v1/analytics';
  static const String shopPath = '/v1/shop';
  static const String memoriesPath = '/v1/memories';
  static const String livePath = '/v1/live';
}
