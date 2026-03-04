import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final followersProvider =
    FutureProvider.autoDispose.family<List<User>, String>((ref, userId) async {
  return ref.watch(userRepositoryProvider).getFollowers(userId);
});

final followingProvider =
    FutureProvider.autoDispose.family<List<User>, String>((ref, userId) async {
  return ref.watch(userRepositoryProvider).getFollowing(userId);
});

final friendsProvider = FutureProvider.autoDispose<List<User>>((ref) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get('/v1/graph/friends');
  final items = (response.data['data'] as List<dynamic>?) ?? [];
  return items
      .map((e) => User.fromJson(e as Map<String, dynamic>))
      .toList();
});
