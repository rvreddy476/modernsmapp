import 'package:atpost_app/data/repositories/notification_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Unread notification count for badge display.
final unreadNotificationCountProvider =
    FutureProvider.autoDispose<int>((ref) async {
  final repo = ref.watch(notificationRepositoryProvider);
  return repo.getUnreadCount();
});

/// Unread chat message count for badge display.
final unreadChatCountProvider = StateProvider<int>((ref) => 0);
