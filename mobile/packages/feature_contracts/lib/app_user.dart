import 'package:flutter_riverpod/flutter_riverpod.dart';

/// A minimal, host-owned projection of the signed-in (or searched) AtPost
/// user. Features that need "who is this person" depend on THIS instead of
/// the app's full User model, so they stay decoupled from the app and from
/// each other. It's a superset of what individual features need (some use
/// only [displayName], some [city], some [avatarUrl]); unused fields are
/// simply null.
class AppUserRef {
  const AppUserRef({
    required this.id,
    required this.displayName,
    this.username = '',
    this.avatarUrl,
    this.city,
  });

  final String id;
  final String displayName;
  final String username;
  final String? avatarUrl;
  final String? city;

  bool get hasAvatar => (avatarUrl ?? '').isNotEmpty;
}

/// The host's signed-in user, or null when signed out / not provided.
/// The app overrides this at its root ProviderScope (see
/// app/feature_host_bindings.dart). Defaults to null so features work in
/// isolation (tests, previews) without host wiring.
final currentAppUserProvider =
    FutureProvider<AppUserRef?>((_) async => null);

/// Search AtPost users by free-text query (people pickers: dating vouch,
/// wallet send, slambook invite). Defaults to no results.
typedef AppUserSearch = Future<List<AppUserRef>> Function(String query);
final appUserSearchProvider =
    Provider<AppUserSearch>((_) => (_) async => const []);

/// Batch-resolve users by id — used to hydrate feed post authors
/// (display name + avatar) when the post payload carries only author_id.
/// Missing ids are simply absent from the result. Defaults to empty so
/// the feed still renders (authors just show as unknown).
typedef AppUserBatchLookup =
    Future<List<AppUserRef>> Function(List<String> ids);
final appUserBatchProvider =
    Provider<AppUserBatchLookup>((_) => (_) async => const []);

/// The set of user ids the given user currently follows — drives the
/// follow/unfollow state on feed + profile surfaces. Returned as a flat
/// id list (the caller sets-ifies). Defaults to empty.
typedef AppFollowingIds = Future<List<String>> Function(String userId);
final appFollowingIdsProvider =
    Provider<AppFollowingIds>((_) => (_) async => const []);
