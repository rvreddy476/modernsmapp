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

  // ── Cloudflare Access service token ────────────────────────────────────
  //
  // Only needed when the API is reached through a hostname sitting behind
  // Cloudflare Access (api.cleestudio.com during testing). Access authenticates
  // humans with a browser login and a cookie; an app making HTTP calls cannot
  // complete that flow and receives the HTML login page instead of JSON. A
  // service token is the machine equivalent: two headers Cloudflare accepts in
  // place of the browser session.
  //
  // This is NOT app authentication. It answers "may this client reach the
  // server at all"; the user is still identified by the normal access token.
  //
  // Both default to empty, so builds that talk to localhost — the usual case —
  // send no extra headers and behave exactly as before.
  //
  // TESTING ONLY. A secret compiled into a mobile binary can be extracted from
  // the package; this is acceptable for private builds on known devices and is
  // not a launch mechanism. At launch the API is public and protected by our
  // own auth, and these stay unset.
  static const String _cfAccessClientId = String.fromEnvironment(
    'CF_ACCESS_CLIENT_ID',
    defaultValue: '',
  );
  static const String _cfAccessClientSecret = String.fromEnvironment(
    'CF_ACCESS_CLIENT_SECRET',
    defaultValue: '',
  );

  /// Headers that get a non-browser client past Cloudflare Access.
  ///
  /// Empty unless BOTH halves are configured — sending one without the other
  /// is never valid, and a half-configured build should fail loudly at the
  /// Access login page rather than look like a server bug.
  static Map<String, String> get accessServiceTokenHeaders {
    final id = _cfAccessClientId.trim();
    final secret = _cfAccessClientSecret.trim();
    if (id.isEmpty || secret.isEmpty) return const {};
    return {'CF-Access-Client-Id': id, 'CF-Access-Client-Secret': secret};
  }

  /// True when this build carries an Access service token. Safe to log —
  /// reports only presence, never the values.
  static bool get hasAccessServiceToken =>
      accessServiceTokenHeaders.isNotEmpty;

  /// Launch kill switch. Calls stay absent from the client unless a release
  /// is explicitly built after real-device/network verification.
  static const bool callsEnabled = bool.fromEnvironment(
    'CALLS_ENABLED',
    defaultValue: false,
  );

  /// Financial mutations remain hidden until the separate KYC, tax,
  /// provider, reconciliation, fraud, and operations checkpoint passes.
  static const bool monetizationWritesEnabled = bool.fromEnvironment(
    'MONETIZATION_WRITES_ENABLED',
    defaultValue: false,
  );

  /// Public crowd moderation stays hidden until reviewer vetting, durable
  /// canonical effects, fraud controls, and payout reconciliation pass.
  static const bool reviewerPublicEnabled = bool.fromEnvironment(
    'REVIEWER_PUBLIC_ENABLED',
    defaultValue: false,
  );

  /// Set to a domain (e.g. "cleestudio.com") to use external HTTPS endpoints.
  /// Leave null for local development with direct service ports.
  static String? externalDomain = _resolveExternalDomain();
  static String? pulseBaseUrlOverride = _trimOrNull(_configuredPulseBaseUrl);

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
