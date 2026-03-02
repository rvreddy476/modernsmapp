import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/notification.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class NotificationRepository {
  final ApiClient _api;

  NotificationRepository(this._api);

  /// Get paginated notifications with cursor.
  Future<NotificationPage> getNotifications({
    int limit = 20,
    String? cursor,
  }) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null) params['cursor'] = cursor;

    final response = await _api.get(
      Environment.notificationsPath,
      queryParameters: params,
    );
    final data = response.data;
    final items = (data['data'] as List<dynamic>?) ?? [];
    final nextCursor = data['meta']?['next_cursor'] as String?;

    return NotificationPage(
      items: items
          .map((e) => AppNotification.fromJson(e as Map<String, dynamic>))
          .toList(),
      nextCursor: nextCursor,
    );
  }

  /// Get unread count.
  Future<int> getUnreadCount() async {
    final response = await _api.get(
      '${Environment.notificationsPath}/unread-count',
    );
    return (response.data['data']?['count'] as int?) ?? 0;
  }

  /// Mark a notification as read.
  Future<void> markRead(int bucket, String ts) async {
    await _api.post(
      '${Environment.notificationsPath}/read',
      data: {'bucket': bucket, 'ts': ts},
    );
  }

  /// Mark all notifications as read.
  Future<void> markAllRead() async {
    await _api.patch('${Environment.notificationsPath}/read-all');
  }
}

class NotificationPage {
  final List<AppNotification> items;
  final String? nextCursor;

  const NotificationPage({required this.items, this.nextCursor});
}

final notificationRepositoryProvider = Provider<NotificationRepository>((ref) {
  return NotificationRepository(ref.watch(apiClientProvider));
});
