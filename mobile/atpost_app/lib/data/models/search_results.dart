// Multi-entity search response models, mirroring the search-service
// ranked search API at GET /v1/search?types=posts,users,hashtags,products,communities,channels.
//
// Each bucket has its own `next_cursor` (opaque, send back as
// `cursor.<entity>=...`). The top-level `queryId` is the analytics
// handle to pass to /v1/search/click on every result tap.

enum SearchEntity {
  posts,
  users,
  hashtags,
  products,
  communities,
  channels,
}

extension SearchEntityX on SearchEntity {
  /// Wire name expected by the backend (`types=`, `cursor.<name>`, etc.)
  String get wire {
    switch (this) {
      case SearchEntity.posts:
        return 'posts';
      case SearchEntity.users:
        return 'users';
      case SearchEntity.hashtags:
        return 'hashtags';
      case SearchEntity.products:
        return 'products';
      case SearchEntity.communities:
        return 'communities';
      case SearchEntity.channels:
        return 'channels';
    }
  }

  /// Human label for tab bars / section headings.
  String get label {
    switch (this) {
      case SearchEntity.posts:
        return 'Posts';
      case SearchEntity.users:
        return 'People';
      case SearchEntity.hashtags:
        return 'Hashtags';
      case SearchEntity.products:
        return 'Products';
      case SearchEntity.communities:
        return 'Communities';
      case SearchEntity.channels:
        return 'Channels';
    }
  }
}

SearchEntity? searchEntityFromWire(String w) {
  for (final e in SearchEntity.values) {
    if (e.wire == w) return e;
  }
  return null;
}

// ─── Per-entity hit models ────────────────────────────────────────────────

class PostHit {
  final String postId;
  final String authorId;
  final String? authorUsername;
  final String text;
  final List<String> hashtags;
  final int likeCount;
  final int commentCount;
  final int shareCount;
  final String? postType;
  final double engagementScore;
  final DateTime? createdAt;

  const PostHit({
    required this.postId,
    required this.authorId,
    this.authorUsername,
    required this.text,
    this.hashtags = const [],
    this.likeCount = 0,
    this.commentCount = 0,
    this.shareCount = 0,
    this.postType,
    this.engagementScore = 0,
    this.createdAt,
  });

  factory PostHit.fromJson(Map<String, dynamic> json) => PostHit(
        postId: (json['post_id'] ?? json['id'] ?? '').toString(),
        authorId: (json['author_id'] ?? '').toString(),
        authorUsername: json['author_username']?.toString(),
        text: (json['text'] ?? json['content'] ?? '').toString(),
        hashtags: (json['hashtags'] as List<dynamic>? ?? const [])
            .map((e) => e.toString())
            .toList(),
        likeCount: (json['like_count'] as num?)?.toInt() ?? 0,
        commentCount: (json['comment_count'] as num?)?.toInt() ?? 0,
        shareCount: (json['share_count'] as num?)?.toInt() ?? 0,
        postType: json['post_type']?.toString(),
        engagementScore: (json['engagement_score'] as num?)?.toDouble() ?? 0,
        createdAt: DateTime.tryParse(json['created_at']?.toString() ?? ''),
      );
}

class UserHit {
  final String userId;
  final String username;
  final String displayName;
  final String? bio;
  final String? avatarMediaId;
  final bool isVerified;
  final int followerCount;
  final int postCount;
  final double engagementScore;

  const UserHit({
    required this.userId,
    required this.username,
    required this.displayName,
    this.bio,
    this.avatarMediaId,
    this.isVerified = false,
    this.followerCount = 0,
    this.postCount = 0,
    this.engagementScore = 0,
  });

  factory UserHit.fromJson(Map<String, dynamic> json) => UserHit(
        userId: (json['user_id'] ?? json['id'] ?? '').toString(),
        username: (json['username'] ?? '').toString(),
        displayName:
            (json['display_name'] ?? json['name'] ?? 'User').toString(),
        bio: json['bio']?.toString(),
        avatarMediaId: json['avatar_media_id']?.toString(),
        isVerified: json['is_verified'] == true,
        followerCount: (json['follower_count'] as num?)?.toInt() ?? 0,
        postCount: (json['post_count'] as num?)?.toInt() ?? 0,
        engagementScore: (json['engagement_score'] as num?)?.toDouble() ?? 0,
      );
}

class HashtagHit {
  final String hashtag;
  final int useCount;
  final double engagementScore;

  const HashtagHit({
    required this.hashtag,
    this.useCount = 0,
    this.engagementScore = 0,
  });

  factory HashtagHit.fromJson(Map<String, dynamic> json) => HashtagHit(
        hashtag: (json['hashtag'] ?? json['tag'] ?? '').toString(),
        useCount: (json['use_count'] as num?)?.toInt() ?? 0,
        engagementScore: (json['engagement_score'] as num?)?.toDouble() ?? 0,
      );
}

class ProductHit {
  final String productId;
  final String sellerId;
  final String title;
  final String? description;
  final String? category;
  final double? price;
  final String? city;
  final String? status;
  final double engagementScore;

  const ProductHit({
    required this.productId,
    required this.sellerId,
    required this.title,
    this.description,
    this.category,
    this.price,
    this.city,
    this.status,
    this.engagementScore = 0,
  });

  factory ProductHit.fromJson(Map<String, dynamic> json) => ProductHit(
        productId: (json['product_id'] ?? json['id'] ?? '').toString(),
        sellerId: (json['seller_id'] ?? '').toString(),
        title: (json['title'] ?? json['name'] ?? '').toString(),
        description: json['description']?.toString(),
        category: json['category']?.toString(),
        price: (json['price'] as num?)?.toDouble(),
        city: json['city']?.toString(),
        status: json['status']?.toString(),
        engagementScore: (json['engagement_score'] as num?)?.toDouble() ?? 0,
      );
}

class CommunityHit {
  final String communityId;
  final String ownerId;
  final String handle;
  final String name;
  final String? description;
  final String communityType;
  final String? category;
  final List<String> topicTags;
  final int memberCount;
  final bool isVerified;
  final double engagementScore;

  const CommunityHit({
    required this.communityId,
    required this.ownerId,
    required this.handle,
    required this.name,
    this.description,
    this.communityType = '',
    this.category,
    this.topicTags = const [],
    this.memberCount = 0,
    this.isVerified = false,
    this.engagementScore = 0,
  });

  factory CommunityHit.fromJson(Map<String, dynamic> json) => CommunityHit(
        communityId: (json['community_id'] ?? json['id'] ?? '').toString(),
        ownerId: (json['owner_id'] ?? '').toString(),
        handle: (json['handle'] ?? '').toString(),
        name: (json['name'] ?? '').toString(),
        description: json['description']?.toString(),
        communityType: (json['community_type'] ?? '').toString(),
        category: json['category']?.toString(),
        topicTags: (json['topic_tags'] as List<dynamic>? ?? const [])
            .map((e) => e.toString())
            .toList(),
        memberCount: (json['member_count'] as num?)?.toInt() ?? 0,
        isVerified: json['is_verified'] == true,
        engagementScore: (json['engagement_score'] as num?)?.toDouble() ?? 0,
      );
}

class ChannelHit {
  final String channelId;
  final String ownerId;
  final String handle;
  final String name;
  final String? description;
  final String channelType;
  final String? category;
  final int subscriberCount;
  final bool isVerified;
  final double engagementScore;

  const ChannelHit({
    required this.channelId,
    required this.ownerId,
    required this.handle,
    required this.name,
    this.description,
    this.channelType = '',
    this.category,
    this.subscriberCount = 0,
    this.isVerified = false,
    this.engagementScore = 0,
  });

  factory ChannelHit.fromJson(Map<String, dynamic> json) => ChannelHit(
        channelId: (json['channel_id'] ?? json['id'] ?? '').toString(),
        ownerId: (json['owner_id'] ?? '').toString(),
        handle: (json['handle'] ?? '').toString(),
        name: (json['name'] ?? '').toString(),
        description: json['description']?.toString(),
        channelType: (json['channel_type'] ?? '').toString(),
        category: json['category']?.toString(),
        subscriberCount: (json['subscriber_count'] as num?)?.toInt() ?? 0,
        isVerified: json['is_verified'] == true,
        engagementScore: (json['engagement_score'] as num?)?.toDouble() ?? 0,
      );
}

// ─── Bucket + envelope ────────────────────────────────────────────────────

class EntityBucket<T> {
  final List<T> items;
  final String? nextCursor;

  const EntityBucket({this.items = const [], this.nextCursor});

  EntityBucket<T> mergeWith(EntityBucket<T> next) {
    return EntityBucket<T>(
      items: [...items, ...next.items],
      nextCursor: next.nextCursor,
    );
  }

  static EntityBucket<T> fromJson<T>(
    Map<String, dynamic>? json,
    T Function(Map<String, dynamic>) fromItem,
  ) {
    if (json == null) return EntityBucket<T>();
    final items = (json['items'] as List<dynamic>? ?? const [])
        .map((e) => fromItem(e as Map<String, dynamic>))
        .toList(growable: false);
    final cursor = json['next_cursor']?.toString();
    return EntityBucket<T>(
      items: items,
      nextCursor: (cursor == null || cursor.isEmpty) ? null : cursor,
    );
  }
}

class MultiEntitySearchResults {
  final String? queryId;
  final EntityBucket<PostHit> posts;
  final EntityBucket<UserHit> users;
  final EntityBucket<HashtagHit> hashtags;
  final EntityBucket<ProductHit> products;
  final EntityBucket<CommunityHit> communities;
  final EntityBucket<ChannelHit> channels;

  const MultiEntitySearchResults({
    this.queryId,
    this.posts = const EntityBucket<PostHit>(),
    this.users = const EntityBucket<UserHit>(),
    this.hashtags = const EntityBucket<HashtagHit>(),
    this.products = const EntityBucket<ProductHit>(),
    this.communities = const EntityBucket<CommunityHit>(),
    this.channels = const EntityBucket<ChannelHit>(),
  });

  /// Total hit count across all buckets — used to pick a default tab.
  int get totalItems =>
      posts.items.length +
      users.items.length +
      hashtags.items.length +
      products.items.length +
      communities.items.length +
      channels.items.length;

  factory MultiEntitySearchResults.fromJson(Map<String, dynamic> json) {
    // The backend wraps the payload in `{data: {...}}` — the
    // repository unwraps `data` before handing the map to this
    // factory. The actual layout is `{query_id, results: {...}}`.
    final results =
        (json['results'] as Map<String, dynamic>?) ?? <String, dynamic>{};
    return MultiEntitySearchResults(
      queryId: json['query_id']?.toString(),
      posts: EntityBucket.fromJson<PostHit>(
        results['posts'] as Map<String, dynamic>?,
        PostHit.fromJson,
      ),
      users: EntityBucket.fromJson<UserHit>(
        results['users'] as Map<String, dynamic>?,
        UserHit.fromJson,
      ),
      hashtags: EntityBucket.fromJson<HashtagHit>(
        results['hashtags'] as Map<String, dynamic>?,
        HashtagHit.fromJson,
      ),
      products: EntityBucket.fromJson<ProductHit>(
        results['products'] as Map<String, dynamic>?,
        ProductHit.fromJson,
      ),
      communities: EntityBucket.fromJson<CommunityHit>(
        results['communities'] as Map<String, dynamic>?,
        CommunityHit.fromJson,
      ),
      channels: EntityBucket.fromJson<ChannelHit>(
        results['channels'] as Map<String, dynamic>?,
        ChannelHit.fromJson,
      ),
    );
  }

  MultiEntitySearchResults copyWith({
    EntityBucket<PostHit>? posts,
    EntityBucket<UserHit>? users,
    EntityBucket<HashtagHit>? hashtags,
    EntityBucket<ProductHit>? products,
    EntityBucket<CommunityHit>? communities,
    EntityBucket<ChannelHit>? channels,
  }) {
    return MultiEntitySearchResults(
      queryId: queryId,
      posts: posts ?? this.posts,
      users: users ?? this.users,
      hashtags: hashtags ?? this.hashtags,
      products: products ?? this.products,
      communities: communities ?? this.communities,
      channels: channels ?? this.channels,
    );
  }
}

// ─── Autocomplete ────────────────────────────────────────────────────────

enum AutocompleteKind { user, hashtag, community, unknown }

AutocompleteKind _parseAutocompleteKind(String? raw) {
  switch (raw) {
    case 'user':
      return AutocompleteKind.user;
    case 'hashtag':
      return AutocompleteKind.hashtag;
    case 'community':
      return AutocompleteKind.community;
    default:
      return AutocompleteKind.unknown;
  }
}

class AutocompleteItem {
  final AutocompleteKind kind;
  // user
  final String? userId;
  final String? username;
  final String? displayName;
  // hashtag
  final String? hashtag;
  // community
  final String? communityId;
  final String? handle;
  final String? name;

  const AutocompleteItem({
    required this.kind,
    this.userId,
    this.username,
    this.displayName,
    this.hashtag,
    this.communityId,
    this.handle,
    this.name,
  });

  factory AutocompleteItem.fromJson(Map<String, dynamic> json) =>
      AutocompleteItem(
        kind: _parseAutocompleteKind(json['kind']?.toString()),
        userId: json['user_id']?.toString(),
        username: json['username']?.toString(),
        displayName: json['display_name']?.toString(),
        hashtag: json['hashtag']?.toString(),
        communityId: json['community_id']?.toString(),
        handle: json['handle']?.toString(),
        name: json['name']?.toString(),
      );
}
