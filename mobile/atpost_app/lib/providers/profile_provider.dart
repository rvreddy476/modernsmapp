import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// State for the Profile feature.
class ProfileState {
  final User? user;
  final List<Post> posts;
  final List<Map<String, dynamic>> pins;
  final List<Map<String, dynamic>> portfolio;
  final bool isLoading;

  const ProfileState({
    this.user,
    this.posts = const [],
    this.pins = const [],
    this.portfolio = const [],
    this.isLoading = false,
  });

  ProfileState copyWith({
    User? user,
    List<Post>? posts,
    List<Map<String, dynamic>>? pins,
    List<Map<String, dynamic>>? portfolio,
    bool? isLoading,
  }) {
    return ProfileState(
      user: user ?? this.user,
      posts: posts ?? this.posts,
      pins: pins ?? this.pins,
      portfolio: portfolio ?? this.portfolio,
      isLoading: isLoading ?? this.isLoading,
    );
  }
}

/// Production-ready Profile Notifier.
class ProfileNotifier extends StateNotifier<AsyncValue<ProfileState>> {
  final UserRepository _userRepo;
  final ApiClient _api;

  ProfileNotifier(this._userRepo, this._api)
    : super(const AsyncValue.loading()) {
    refresh();
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    try {
      final user = await ErrorHandler.retry(() => _userRepo.getMe());

      final results = await Future.wait([
        ErrorHandler.retry(
          () => _api.get(
            '/v1/posts/by-author/${user.id}',
            queryParameters: {'limit': 30},
          ),
        ),
        ErrorHandler.retry(() => _api.get('/v1/users/${user.id}/pins')),
        ErrorHandler.retry(() => _api.get('/v1/users/${user.id}/portfolio')),
      ]);

      final postsRes = results[0];
      final pinsRes = results[1];
      final portfolioRes = results[2];

      final postsData = postsRes.data['data'];
      final List rawPosts = postsData is List
          ? postsData
          : (postsData is Map && postsData['items'] is List)
              ? postsData['items'] as List
              : const [];
      final posts = rawPosts
          .map((e) => Post.fromJson(e as Map<String, dynamic>))
          .toList();

      List<Map<String, dynamic>> asList(dynamic d) {
        if (d is List) return d.cast<Map<String, dynamic>>();
        if (d is Map && d['items'] is List) {
          return (d['items'] as List).cast<Map<String, dynamic>>();
        }
        return const [];
      }

      final pins = asList(pinsRes.data['data']);
      final portfolio = asList(portfolioRes.data['data']);

      state = AsyncValue.data(
        ProfileState(
          user: user,
          posts: posts,
          pins: pins,
          portfolio: portfolio,
        ),
      );
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }
}

final profileProvider =
    StateNotifierProvider.autoDispose<
      ProfileNotifier,
      AsyncValue<ProfileState>
    >((ref) {
      return ProfileNotifier(
        ref.watch(userRepositoryProvider),
        ref.watch(apiClientProvider),
      );
    });
