import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Active sub-tab inside the Home feed screen: 0 = For You, 1 = Following,
/// 2 = #Hashtag. Lifted out of HomeFeedScreen's local state so other
/// surfaces (PostCard's clickable hashtags, via the appHashtagTap host
/// binding) can switch the Home tab.
final homeFeedTabProvider = StateProvider<int>((ref) => 0);
