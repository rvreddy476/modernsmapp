class ProfilePin {
  final String id;
  final String userId;
  final String contentType;
  final String contentId;
  final int displayOrder;
  final DateTime createdAt;

  const ProfilePin({
    required this.id,
    required this.userId,
    required this.contentType,
    required this.contentId,
    required this.displayOrder,
    required this.createdAt,
  });

  factory ProfilePin.fromJson(Map<String, dynamic> json) => ProfilePin(
        id: json['id'] as String? ?? '',
        userId: json['user_id'] as String? ?? '',
        contentType: json['content_type'] as String? ?? '',
        contentId: json['content_id'] as String? ?? '',
        displayOrder: json['display_order'] as int? ?? 0,
        createdAt: json['created_at'] != null
            ? DateTime.parse(json['created_at'] as String)
            : DateTime.now(),
      );
}

class PortfolioItem {
  final String id;
  final String userId;
  final String title;
  final String? description;
  final String? url;
  final String? mediaUrl;
  final String itemType;
  final int displayOrder;
  final DateTime createdAt;

  const PortfolioItem({
    required this.id,
    required this.userId,
    required this.title,
    this.description,
    this.url,
    this.mediaUrl,
    required this.itemType,
    required this.displayOrder,
    required this.createdAt,
  });

  factory PortfolioItem.fromJson(Map<String, dynamic> json) => PortfolioItem(
        id: json['id'] as String? ?? '',
        userId: json['user_id'] as String? ?? '',
        title: json['title'] as String? ?? '',
        description: json['description'] as String?,
        url: json['url'] as String?,
        mediaUrl: json['media_url'] as String?,
        itemType: json['item_type'] as String? ?? 'project',
        displayOrder: json['display_order'] as int? ?? 0,
        createdAt: json['created_at'] != null
            ? DateTime.parse(json['created_at'] as String)
            : DateTime.now(),
      );
}

class ProfileQrCode {
  final String id;
  final String userId;
  final String qrUrl;
  final String profileUrl;
  final int scanCount;
  final DateTime createdAt;

  const ProfileQrCode({
    required this.id,
    required this.userId,
    required this.qrUrl,
    required this.profileUrl,
    required this.scanCount,
    required this.createdAt,
  });

  factory ProfileQrCode.fromJson(Map<String, dynamic> json) => ProfileQrCode(
        id: json['id'] as String? ?? '',
        userId: json['user_id'] as String? ?? '',
        qrUrl: json['qr_url'] as String? ?? '',
        profileUrl: json['profile_url'] as String? ?? '',
        scanCount: json['scan_count'] as int? ?? 0,
        createdAt: json['created_at'] != null
            ? DateTime.parse(json['created_at'] as String)
            : DateTime.now(),
      );
}
