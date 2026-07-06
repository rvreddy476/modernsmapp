import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Index of the active bottom-nav tab inside ShellScaffold.
///
/// Layout (5-tab super-app shell, 2026-05):
///   0 — Home
///   1 — Search
///   2 — Inbox
///   3 — Me
/// (`Create` is a center FAB, not a tab — it doesn't move this index.)
///
/// Lives in its own file to keep widgets that change the tab from having to
/// import shell_scaffold.dart (which would create cycles because the
/// scaffold imports many feature screens).
final shellTabProvider = StateProvider<int>((ref) => 0);

/// LEGACY: kept for binary-compat with the previous shell which had a 5-way
/// nav and an inline create menu. The new shell opens a modal bottom sheet
/// (`CreateOptionsSheet`) instead. Nothing in the new shell reads this, but
/// some older widgets may still write to it; we leave the StateProvider in
/// place to avoid breaking imports during the migration.
final createMenuOpenProvider = StateProvider<bool>((ref) => false);

// The Home feed's sub-tab state (For You / Following / #Hashtag) moved to
// feature_home/home_providers.dart (homeFeedTabProvider) when Home was
// extracted into its own package.
