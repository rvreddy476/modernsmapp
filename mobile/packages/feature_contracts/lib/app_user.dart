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
