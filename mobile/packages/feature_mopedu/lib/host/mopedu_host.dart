import 'package:flutter_riverpod/flutter_riverpod.dart';

/// What feature_mopedu needs from its host app — the signed-in user's
/// display name (onboarding prefill) and city (the Mopedu city gate).
/// The host binds [mopeduHostUserProvider] at its root ProviderScope;
/// the feature never imports the app's User model or user provider.
class MopeduHostUser {
  const MopeduHostUser({
    required this.id,
    required this.displayName,
    this.city,
  });

  final String id;
  final String displayName;
  final String? city;
}

/// The host's signed-in user, or null when signed out / not provided.
/// Defaults to null so the mopedu screens work (city gate falls back to
/// its own selected-city path) without host wiring.
final mopeduHostUserProvider =
    FutureProvider<MopeduHostUser?>((_) async => null);
