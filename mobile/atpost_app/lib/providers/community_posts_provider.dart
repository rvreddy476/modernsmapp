import 'package:atpost_app/data/models/community.dart';
import 'package:atpost_app/data/models/community_post.dart';
import 'package:atpost_app/data/repositories/community_posts_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Params for fetching posts in a specific community space.
class CommunityPostsParams {
  final String communityId;
  final String spaceId;

  const CommunityPostsParams({
    required this.communityId,
    required this.spaceId,
  });

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is CommunityPostsParams &&
          communityId == other.communityId &&
          spaceId == other.spaceId;

  @override
  int get hashCode => Object.hash(communityId, spaceId);
}

final communityPostsProvider = FutureProvider.autoDispose
    .family<List<CommunityPost>, CommunityPostsParams>((ref, params) async {
  return ref
      .watch(communityPostsRepositoryProvider)
      .listSpacePosts(params.communityId, params.spaceId);
});

final communitySpacesListProvider = FutureProvider.autoDispose
    .family<List<CommunitySpace>, String>((ref, communityId) async {
  return ref
      .watch(communityPostsRepositoryProvider)
      .listSpaces(communityId);
});

final wikiPagesProvider = FutureProvider.autoDispose
    .family<List<WikiPage>, String>((ref, communityId) async {
  return ref
      .watch(communityPostsRepositoryProvider)
      .listWikiPages(communityId);
});
