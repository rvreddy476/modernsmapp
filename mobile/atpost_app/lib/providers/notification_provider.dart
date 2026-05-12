import 'package:atpost_app/data/models/notification.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:atpost_app/data/repositories/notification_repository.dart';
import 'package:atpost_app/services/realtime_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Unread notification count for badge display.
final unreadNotificationCountProvider = FutureProvider.autoDispose<int>((
  ref,
) async {
  final repo = ref.watch(notificationRepositoryProvider);
  return repo.getUnreadCount();
});

/// Live notification stream — emits exactly the NotificationEvent
/// items off the shared WS multiplex. Consumers:
///   - the shell scaffold listens via ref.listen and shows a toast
///   - this same listener invalidates the bell + list providers below
///     so the badge and inbox refresh without an explicit fetch
///
/// Kept as a non-broadcast StreamProvider so each listener gets every
/// event; broadcast semantics come from RealtimeService.events itself.
final liveNotificationsProvider = StreamProvider<NotificationEvent>((ref) {
  final realtime = ref.watch(realtimeServiceProvider);
  return realtime.events
      .where((e) => e is NotificationEvent)
      .cast<NotificationEvent>();
});

/// Unread chat message count for badge display.
final unreadChatCountProvider = FutureProvider.autoDispose<int>((ref) async {
  final repo = ref.watch(chatRepositoryProvider);
  return repo.getUnreadCount();
});

/// Full notifications list provider.
final notificationsProvider = FutureProvider.autoDispose<List<AppNotification>>(
  (ref) async {
    final repo = ref.watch(notificationRepositoryProvider);
    final page = await repo.getNotifications(limit: 50);
    return page.items;
  },
);
