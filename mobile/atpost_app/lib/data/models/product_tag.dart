// Mirrors Architecture/services/post-service/internal/store/postgres/
// product_tags.go::PostProductTag. Both time + position fields are
// nullable — null time = "whole video", null position = "player picks".

class PostProductTag {
  PostProductTag({
    required this.id,
    required this.postId,
    required this.affiliateLinkId,
    required this.creatorId,
    required this.label,
    required this.imageUrl,
    required this.impressionCount,
    required this.clickCount,
    required this.isActive,
    this.timeStartMs,
    this.timeEndMs,
    this.positionX,
    this.positionY,
    this.createdAt,
    this.updatedAt,
  });

  final String id;
  final String postId;
  final String affiliateLinkId;
  final String creatorId;
  final int? timeStartMs;
  final int? timeEndMs;
  // 0..100, percentage of player viewport. Null = player default.
  final double? positionX;
  final double? positionY;
  final String label;
  final String imageUrl;
  final int impressionCount;
  final int clickCount;
  final bool isActive;
  final DateTime? createdAt;
  final DateTime? updatedAt;

  factory PostProductTag.fromJson(Map<String, dynamic> json) => PostProductTag(
        id: json['id'] as String,
        postId: json['post_id'] as String,
        affiliateLinkId: json['affiliate_link_id'] as String,
        creatorId: json['creator_id'] as String,
        timeStartMs: (json['time_start_ms'] as num?)?.toInt(),
        timeEndMs: (json['time_end_ms'] as num?)?.toInt(),
        positionX: (json['position_x'] as num?)?.toDouble(),
        positionY: (json['position_y'] as num?)?.toDouble(),
        label: (json['label'] as String?) ?? '',
        imageUrl: (json['image_url'] as String?) ?? '',
        impressionCount: (json['impression_count'] as num?)?.toInt() ?? 0,
        clickCount: (json['click_count'] as num?)?.toInt() ?? 0,
        isActive: (json['is_active'] as bool?) ?? true,
        createdAt: json['created_at'] != null
            ? DateTime.tryParse(json['created_at'] as String)
            : null,
        updatedAt: json['updated_at'] != null
            ? DateTime.tryParse(json['updated_at'] as String)
            : null,
      );
}
