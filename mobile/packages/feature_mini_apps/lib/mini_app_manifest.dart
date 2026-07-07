class MiniAppManifest {
  final Uri startUri;
  final Set<String> allowedOrigins;
  final String? bridgeVersion;
  final String? version;

  const MiniAppManifest({
    required this.startUri,
    required this.allowedOrigins,
    this.bridgeVersion,
    this.version,
  });

  factory MiniAppManifest.fromJson(
    Map<String, dynamic> json, {
    required Uri manifestUri,
  }) {
    final rawStartUrl = (json['start_url'] ?? json['startUrl'] ?? '')
        .toString()
        .trim();
    if (rawStartUrl.isEmpty) {
      throw const FormatException('Mini app manifest is missing start_url');
    }

    final startUri = manifestUri.resolve(rawStartUrl);
    if (!_isNetworkUri(startUri)) {
      throw const FormatException(
        'Mini app start_url must resolve to an absolute http or https URL',
      );
    }

    return MiniAppManifest(
      startUri: startUri,
      allowedOrigins: _parseAllowedOrigins(
        json['allowed_origins'] ?? json['allowedOrigins'],
        manifestUri: manifestUri,
        startUri: startUri,
      ),
      bridgeVersion: _asNullableString(
        json['bridge_version'] ?? json['bridgeVersion'],
      ),
      version: _asNullableString(json['version']),
    );
  }
}

class MiniAppLaunchConfig {
  final Uri entryUri;
  final Uri sourceUri;
  final Set<String> allowedOrigins;
  final bool fromManifest;
  final String? bridgeVersion;

  const MiniAppLaunchConfig({
    required this.entryUri,
    required this.sourceUri,
    required this.allowedOrigins,
    required this.fromManifest,
    this.bridgeVersion,
  });

  factory MiniAppLaunchConfig.legacy({required Uri entryUri}) {
    final entryOrigin = _originFor(entryUri);
    return MiniAppLaunchConfig(
      entryUri: entryUri,
      sourceUri: entryUri,
      allowedOrigins: {if (entryOrigin.isNotEmpty) entryOrigin},
      fromManifest: false,
    );
  }

  factory MiniAppLaunchConfig.fromManifest({
    required Uri manifestUri,
    required MiniAppManifest manifest,
  }) {
    final manifestOrigin = _originFor(manifestUri);
    return MiniAppLaunchConfig(
      entryUri: manifest.startUri,
      sourceUri: manifestUri,
      allowedOrigins: {
        ...manifest.allowedOrigins,
        if (manifestOrigin.isNotEmpty) manifestOrigin,
      },
      fromManifest: true,
      bridgeVersion: manifest.bridgeVersion,
    );
  }

  bool allows(Uri? uri) {
    if (uri == null) return false;
    if (_isInternalScheme(uri)) return true;

    final origin = _originFor(uri);
    return origin.isNotEmpty && allowedOrigins.contains(origin);
  }
}

String? _asNullableString(dynamic value) {
  final text = value?.toString().trim();
  if (text == null || text.isEmpty) return null;
  return text;
}

Set<String> _parseAllowedOrigins(
  dynamic value, {
  required Uri manifestUri,
  required Uri startUri,
}) {
  final origins = <String>{};

  void addOrigin(dynamic rawValue) {
    final raw = rawValue?.toString().trim() ?? '';
    if (raw.isEmpty) return;

    final parsed = Uri.tryParse(raw);
    final resolved = parsed != null && parsed.hasScheme
        ? parsed
        : manifestUri.resolve(raw);
    final origin = _originFor(resolved);
    if (origin.isNotEmpty) {
      origins.add(origin);
    }
  }

  if (value is List) {
    for (final item in value) {
      addOrigin(item);
    }
  }

  final manifestOrigin = _originFor(manifestUri);
  if (manifestOrigin.isNotEmpty) {
    origins.add(manifestOrigin);
  }

  final startOrigin = _originFor(startUri);
  if (startOrigin.isNotEmpty) {
    origins.add(startOrigin);
  }

  return origins;
}

bool _isInternalScheme(Uri uri) {
  switch (uri.scheme) {
    case 'about':
    case 'data':
    case 'blob':
    case 'javascript':
      return true;
    default:
      return false;
  }
}

bool _isNetworkUri(Uri uri) {
  return (uri.scheme == 'http' || uri.scheme == 'https') && uri.host.isNotEmpty;
}

String _originFor(Uri uri) {
  if (!_isNetworkUri(uri)) return '';
  final port = uri.hasPort ? ':${uri.port}' : '';
  return '${uri.scheme}://${uri.host}$port';
}
