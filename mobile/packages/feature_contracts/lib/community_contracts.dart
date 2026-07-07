import 'package:flutter_riverpod/flutter_riverpod.dart';

/// A minimal host projection of a community the signed-in user belongs to,
/// used by pickers (e.g. choosing which community to post a question in).
/// Features depend on this instead of the app's full Community model +
/// repository, so they stay decoupled from the community stack.
class AppCommunityRef {
  const AppCommunityRef({required this.id, required this.name});
  final String id;
  final String name;
}

/// The signed-in user's communities, for community pickers. The app binds
/// this to its communities provider; defaults to an empty (loaded) list so
/// a picker renders the "no community" option only.
final appMyCommunitiesProvider =
    Provider<AsyncValue<List<AppCommunityRef>>>((_) => const AsyncData([]));
