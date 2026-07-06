import 'dart:io' show Platform;

import 'package:flutter/foundation.dart' show kIsWeb, kDebugMode;

/// Environment configuration for API endpoints.
///
/// Use `ATPOST_EXTERNAL_DOMAIN` to route all traffic through Caddy/Cloudflare.
/// Leave it unset in native builds to use direct local ports instead.
class Environment {
  const Environment._();

  static const String _defaultExternalDomain = 'cleestudio.com';
  static const String _configuredExternalDomain = String.fromEnvironment(
    'ATPOST_EXTERNAL_DOMAIN',
    defaultValue: '',
  );
  static const String _configuredDirectHost = String.fromEnvironment(
    'ATPOST_DIRECT_HOST',
    defaultValue: '',
  );
  static const String _configuredApiBaseUrl = String.fromEnvironment(
    'ATPOST_API_BASE_URL',
    defaultValue: '',
  );
  static const String _configuredPulseBaseUrl = String.fromEnvironment(
    'ATPOST_PULSE_BASE_URL',
    defaultValue: '',
  );
  static const String _configuredWsBaseUrl = String.fromEnvironment(
    'ATPOST_WS_BASE_URL',
    defaultValue: '',
  );

  /// Set to a domain (e.g. "cleestudio.com") to use external HTTPS endpoints.
  /// Leave null for local development with direct service ports.
  static String? externalDomain = _resolveExternalDomain();
  static String? pulseBaseUrlOverride = _trimOrNull(
    _configuredPulseBaseUrl,
  );

  // Android debug defaults to adb-reversed localhost on a physical device.
  // Override with ATPOST_DIRECT_HOST=10.0.2.2 when targeting an emulator.
  static String get _host {
    final configuredHost = _trimOrNull(_configuredDirectHost);
    if (configuredHost != null) return configuredHost;
    if (kIsWeb) return 'localhost';
    try {
      if (Platform.isAndroid) return '127.0.0.1';
    } catch (_) {}
    return 'localhost';
  }

  static String? _resolveExternalDomain() {
    final configuredDomain = _trimOrNull(_configuredExternalDomain);
    if (configuredDomain != null) {
      return configuredDomain;
    }
    if (_trimOrNull(_configuredApiBaseUrl) != null ||
        _trimOrNull(_configuredWsBaseUrl) != null) {
      return null;
    }
    // Debug builds default to LOCAL services (direct ports / _host), so a plain
    // `flutter run` talks to the local stack instead of production. Release builds
    // use the production domain. Override either with ATPOST_EXTERNAL_DOMAIN /
    // ATPOST_API_BASE_URL.
    return kDebugMode ? null : _defaultExternalDomain;
  }

  static String? _trimOrNull(String? value) {
    final trimmed = value?.trim();
    if (trimmed == null || trimmed.isEmpty) {
      return null;
    }
    return trimmed;
  }

  // Base URLs - auto-detect platform, or use explicit overrides if set.
  static String get apiBaseUrl {
    final override = _trimOrNull(_configuredApiBaseUrl);
    if (override != null) return override;
    if (externalDomain != null) return 'https://$externalDomain';
    return 'http://$_host:8080';
  }

  static String get pulseBaseUrl {
    final directOverride = _trimOrNull(_configuredPulseBaseUrl);
    if (directOverride != null) {
      return directOverride;
    }
    final override = pulseBaseUrlOverride?.trim();
    if (override != null && override.isNotEmpty) {
      return override;
    }
    if (externalDomain != null) return 'https://$externalDomain';
    return 'http://$_host:8090';
  }

  static Uri get wsGatewayUri {
    final override = _trimOrNull(_configuredWsBaseUrl);
    if (override != null) {
      return Uri.parse(override);
    }
    if (externalDomain != null) {
      return Uri(scheme: 'wss', host: externalDomain!, path: '/v1/ws/connect');
    }
    return Uri(scheme: 'ws', host: _host, port: 8093, path: '/v1/ws/connect');
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
  static const String foodPath = '/v1/food';
  static const String memoriesPath = '/v1/memories';
  static const String livePath = '/v1/live';
  // Live streaming v2 (LiveKit / live-service-v2). Separate prefix
  // from the legacy v1 service to avoid route collisions at the gateway.
  static const String liveV2Path = '/v1/livestream';
}
