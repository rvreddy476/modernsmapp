import 'package:atpost_app/data/models/notification.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:atpost_app/data/repositories/notification_repository.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/notification_stream_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Unread notification count for badge display.
final unreadNotificationCountProvider = FutureProvider.autoDispose<int>((
  ref,
) async {
  final repo = ref.watch(notificationRepositoryProvider);
  return repo.getUnreadCount();
});

/// Owns the dedicated SSE connection to /v1/notifications/stream.
/// Created once per app lifecycle and kept alive — the service
/// internally reconnects with exponential backoff and persists
/// Last-Event-ID across reconnects, so this provider is fire-and-
/// forget. Disposed only when the provider container itself shuts
/// down (i.e. process termination).
final notificationStreamServiceProvider =
    Provider<NotificationStreamService>((ref) {
  final auth = ref.watch(authServiceProvider);
  final service = NotificationStreamService(auth);
  service.start();
  ref.onDispose(service.dispose);
  return service;
});

/// Live notification stream — sources NotificationEvent items from
/// the dedicated SSE connection (not the WS multiplex). Consumers:
///   - the shell scaffold listens via ref.listen and shows a toast
///   - this same listener invalidates the bell + list providers
///     below so the badge and inbox refresh without an explicit fetch
final liveNotificationsProvider = StreamProvider<NotificationEvent>((ref) {
  return ref.watch(notificationStreamServiceProvider).events;
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
