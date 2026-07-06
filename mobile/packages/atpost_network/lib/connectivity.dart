import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Simple connectivity state provider.
///
/// Set to `true` when a [NetworkException] occurs, and back to `false`
/// when any API request succeeds. Screens can watch this to show an
/// offline banner.
final isOfflineProvider = StateProvider<bool>((ref) => false);
