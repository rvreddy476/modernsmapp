import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Cached set of user IDs the viewer currently follows. Used by:
/// - PostCard's follow button to render "Following" instead of "Follow"
///   for already-followed authors.
/// - The home Following tab, which client-side filters the feed to posts
///   whose author appears in this set.
///
/// Invalidate after a successful follow/unfollow to keep both consumers
/// in sync.
class FollowingNotifier extends StateNotifier<AsyncValue<Set<String>>> {
  FollowingNotifier(this._repo, this._auth)
      : super(const AsyncValue.loading()) {
    refresh();
  }

  final UserRepository _repo;
  final AuthService _auth;

  Future<void> refresh() async {
    final me = _auth.userId;
    if (me == null || me.isEmpty) {
      state = const AsyncValue.data(<String>{});
      return;
    }
    try {
      final ids = await _repo.getFollowingIds(me);
      state = AsyncValue.data(ids.toSet());
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  /// Optimistic local mutations - do these alongside the API call so the UI
  /// reflects the change immediately. The server-truth refresh re-runs on
  /// next provider invalidation.
  void markFollowing(String userId) {
    final current = state.valueOrNull ?? <String>{};
    state = AsyncValue.data({...current, userId});
  }

  void markUnfollowing(String userId) {
    final current = state.valueOrNull ?? <String>{};
    state = AsyncValue.data(current.where((id) => id != userId).toSet());
  }
}

final followingProvider =
    StateNotifierProvider<FollowingNotifier, AsyncValue<Set<String>>>((ref) {
  return FollowingNotifier(
    ref.watch(userRepositoryProvider),
    ref.watch(authServiceProvider),
  );
});
