import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class FeedRepository {
  final ApiClient _api;

  FeedRepository(this._api);

  /// Fetch home feed with pagination.
  Future<List<Post>> getHomeFeed({
    int limit = 20,
    String feedMode = 'ranked',
    String? cursor,
  }) async {
    final params = <String, dynamic>{
      'limit': limit,
      'feed_mode': feedMode,
      'exclude_self': true,
    };
    if (cursor != null) params['cursor'] = cursor;

    final response = await _api.get(
      '${Environment.feedPath}/home',
      queryParameters: params,
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items
        .map((e) => Post.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Fetch reel feed.
  Future<List<Post>> getReelFeed({int limit = 20, String? cursor}) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null) params['cursor'] = cursor;

    final response = await _api.get(
      '${Environment.feedPath}/reels',
      queryParameters: params,
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items
        .map((e) => Post.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Fetch video (PostTube) feed.
  Future<List<Post>> getVideoFeed({int limit = 20, String? cursor}) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null) params['cursor'] = cursor;

    final response = await _api.get(
      '${Environment.feedPath}/watch',
      queryParameters: params,
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items
        .map((e) => Post.fromJson(e as Map<String, dynamic>))
        .toList();
  }
}

final feedRepositoryProvider = Provider<FeedRepository>((ref) {
  return FeedRepository(ref.watch(apiClientProvider));
});
