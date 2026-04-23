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
      final results = await Future.wait([
        ErrorHandler.retry(() => _userRepo.getMe()),
        ErrorHandler.retry(
          () => _api.get(
            '/v1/posts',
            queryParameters: {'author_id': 'me', 'limit': 30},
          ),
        ),
        ErrorHandler.retry(() => _api.get('/v1/users/me/pins')),
        ErrorHandler.retry(() => _api.get('/v1/users/me/portfolio')),
      ]);

      final user = results[0] as User;
      final postsRes = results[1] as dynamic;
      final pinsRes = results[2] as dynamic;
      final portfolioRes = results[3] as dynamic;

      final posts =
          (postsRes.data['data']?['items'] as List?)
              ?.map((e) => Post.fromJson(e))
              .toList() ??
          [];
      final pins =
          (pinsRes.data['data'] as List?)?.cast<Map<String, dynamic>>() ?? [];
      final portfolio =
          (portfolioRes.data['data'] as List?)?.cast<Map<String, dynamic>>() ??
          [];

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
