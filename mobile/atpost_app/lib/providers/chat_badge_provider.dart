import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Unread chat message count for badge display. Lives in the app (not
/// feature_notifications) because it reads the app's ChatRepository; the
/// home badge binding (appUnreadChatProvider) watches this.
final unreadChatCountProvider = FutureProvider.autoDispose<int>((ref) async {
  final repo = ref.watch(chatRepositoryProvider);
  return repo.getUnreadCount();
});
