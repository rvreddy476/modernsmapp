import 'package:atpost_app/data/models/community.dart';
import 'package:atpost_app/data/repositories/communities_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final myCommunitiesProvider =
    FutureProvider.autoDispose<List<Community>>((ref) async {
  return ref.watch(communitiesRepositoryProvider).getMyCommunities();
});

final discoverCommunitiesProvider =
    FutureProvider.autoDispose<List<Community>>((ref) async {
  return ref.watch(communitiesRepositoryProvider).discoverCommunities();
});

final communityDetailProvider = FutureProvider.autoDispose
    .family<Community, String>((ref, communityId) async {
  return ref.watch(communitiesRepositoryProvider).getCommunity(communityId);
});

final communitySpacesProvider = FutureProvider.autoDispose
    .family<List<CommunitySpace>, String>((ref, communityId) async {
  return ref.watch(communitiesRepositoryProvider).getSpaces(communityId);
});
