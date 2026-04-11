import 'package:atpost_app/core/utils/app_logger.dart';

/// Production-ready MiniApp model with total resilience logic.
class MiniApp {
  final String id;
  final String developerId;
  final String name;
  final String description;
  final String? iconUrl;
  final String manifestUrl;
  final List<String> permissions;
  final String status;
  final String? category;
  final int installCount;
  final DateTime createdAt;
  final bool isInstalled;
  final List<String> grantedPermissions;

  const MiniApp({
    required this.id,
    required this.developerId,
    required this.name,
    required this.description,
    this.iconUrl,
    required this.manifestUrl,
    required this.permissions,
    required this.status,
    this.category,
    required this.installCount,
    required this.createdAt,
    this.isInstalled = false,
    this.grantedPermissions = const [],
  });

  factory MiniApp.fromJson(
    Map<String, dynamic> json, {
    bool isInstalled = false,
  }) {
    try {
      return MiniApp(
        id: (json['id'] ?? '').toString(),
        developerId: (json['developer_id'] ?? '').toString(),
        name: (json['name'] ?? 'Untitled App').toString(),
        description: (json['description'] ?? '').toString(),
        iconUrl: json['icon_url']?.toString(),
        manifestUrl: (json['manifest_url'] ?? '').toString(),
        permissions: _parseList<String>(json['permissions']),
        status: (json['status'] ?? 'active').toString(),
        category: json['category']?.toString(),
        installCount: _toInt(json['install_count']),
        createdAt: _parseDate(json['created_at']),
        isInstalled: isInstalled,
        grantedPermissions: _parseList<String>(json['granted_permissions']),
      );
    } catch (e, st) {
      AppLogger.error('MiniApp.fromJson failed', error: e, stackTrace: st);
      return MiniApp.empty();
    }
  }

  static MiniApp empty() => MiniApp(
    id: 'error',
    developerId: '',
    name: 'App Unavailable',
    description: '',
    manifestUrl: '',
    permissions: const [],
    status: 'error',
    installCount: 0,
    createdAt: DateTime.now(),
  );

  MiniApp copyWith({
    bool? isInstalled,
    int? installCount,
    List<String>? grantedPermissions,
  }) {
    return MiniApp(
      id: id,
      developerId: developerId,
      name: name,
      description: description,
      iconUrl: iconUrl,
      manifestUrl: manifestUrl,
      permissions: permissions,
      status: status,
      category: category,
      installCount: installCount ?? this.installCount,
      createdAt: createdAt,
      isInstalled: isInstalled ?? this.isInstalled,
      grantedPermissions: grantedPermissions ?? this.grantedPermissions,
    );
  }

  MiniApp withInstalledStateFrom(MiniApp? installedApp) {
    if (installedApp == null || installedApp.id != id) {
      return copyWith(isInstalled: false, grantedPermissions: const []);
    }

    return copyWith(
      isInstalled: true,
      grantedPermissions: installedApp.grantedPermissions,
    );
  }
}

// --- Resilience Helpers ---

List<T> _parseList<T>(dynamic data) {
  if (data is List) return data.cast<T>();
  return const [];
}

int _toInt(dynamic data) {
  if (data is int) return data;
  if (data is String) return int.tryParse(data) ?? 0;
  return 0;
}

DateTime _parseDate(dynamic data) {
  if (data is String) return DateTime.tryParse(data) ?? DateTime.now();
  return DateTime.now();
}
