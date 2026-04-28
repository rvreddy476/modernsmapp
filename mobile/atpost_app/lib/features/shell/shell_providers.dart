import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Index of the active bottom-nav tab inside ShellScaffold.
/// Lives in its own file to keep widgets that change the tab from
/// having to import shell_scaffold.dart (which would create cycles
/// because shell_scaffold imports many feature screens).
final shellTabProvider = StateProvider<int>((ref) => 0);

/// Whether the create-action sheet (the + button menu) is open.
final createMenuOpenProvider = StateProvider<bool>((ref) => false);

/// Active sub-tab inside the Home screen: 0 = For You, 1 = Following,
/// 2 = #Hashtag. Lifted out of HomeFeedScreen's local state so other widgets
/// (e.g. PostCard's clickable hashtags) can switch tabs.
final homeFeedTabProvider = StateProvider<int>((ref) => 0);
