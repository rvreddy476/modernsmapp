/// A hashtag suggestion / trending entry returned by the backend.
class HashtagModel {
  const HashtagModel({
    required this.normalizedName,
    required this.displayName,
    required this.postCount,
    this.isTrending = false,
  });

  final String normalizedName;
  final String displayName;
  final int postCount;
  final bool isTrending;

  factory HashtagModel.fromJson(Map<String, dynamic> json) {
    return HashtagModel(
      normalizedName: (json['normalized_name'] ?? json['normalizedName'] ?? '')
          .toString(),
      displayName:
          (json['display_name'] ?? json['displayName'] ?? '').toString(),
      postCount: _toInt(json['post_count'] ?? json['postCount']),
      isTrending: json['is_trending'] == true || json['isTrending'] == true,
    );
  }

  static int _toInt(dynamic v) {
    if (v is int) return v;
    if (v is double) return v.toInt();
    if (v is String) return int.tryParse(v) ?? 0;
    return 0;
  }
}
