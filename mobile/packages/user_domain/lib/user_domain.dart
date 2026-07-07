/// Barrel for the user + social-graph domain: the rich User model, the
/// user repository (profiles, search, follow/mute/block, batch lookup),
/// and the social-graph providers (followers / following / friends /
/// requests / presence / suggestions). Features that need the full user
/// or the social graph depend on this; lighter consumers use the minimal
/// projections in feature_contracts instead.
library;

export 'social_provider.dart';
export 'user.dart';
export 'user_provider.dart';
export 'user_repository.dart';
