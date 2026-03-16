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
  });

  factory MiniApp.fromJson(Map<String, dynamic> json) => MiniApp(
        id: json['id'] as String? ?? '',
        developerId: json['developer_id'] as String? ?? '',
        name: json['name'] as String? ?? '',
        description: json['description'] as String? ?? '',
        iconUrl: json['icon_url'] as String?,
        manifestUrl: json['manifest_url'] as String? ?? '',
        permissions:
            (json['permissions'] as List<dynamic>?)?.cast<String>() ?? [],
        status: json['status'] as String? ?? '',
        category: json['category'] as String?,
        installCount: json['install_count'] as int? ?? 0,
        createdAt: json['created_at'] != null
            ? DateTime.parse(json['created_at'] as String)
            : DateTime.now(),
      );
}
