import 'package:atpost_app/data/models/notification.dart';
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

/// Full notifications list provider.
final notificationsProvider =
    FutureProvider.autoDispose<List<AppNotification>>((ref) async {
  final repo = ref.watch(notificationRepositoryProvider);
  final page = await repo.getNotifications(limit: 50);
  return page.items;
});
