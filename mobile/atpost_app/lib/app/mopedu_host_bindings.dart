import 'package:atpost_app/providers/user_provider.dart';
import 'package:feature_mopedu/host/mopedu_host.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// App-side implementation of feature_mopedu's host contract — the
/// signed-in user's display name (onboarding prefill) and city (the
/// Mopedu city gate). The ONLY place the app's user provider meets the
/// mopedu feature.
List<Override> mopeduHostBindings() => [
  mopeduHostUserProvider.overrideWith((ref) async {
    final user = await ref.watch(currentUserProvider.future);
    return MopeduHostUser(
      id: user.id,
      displayName: user.displayName,
      city: user.location,
    );
  }),
];
