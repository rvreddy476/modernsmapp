// Unified inbox providers — AtPost super-app shell.
//
// The shell's Inbox tab merges five upstream sources into one timeline:
//
//   - notifications-service     mentions, follows, likes (existing)
//   - pulse-service             matches + sparks (existing)
//   - commerce / orders         status updates (existing)
//   - chat-service              direct-message previews (existing unread)
//   - notification-service      `system` bucket (the catch-all)
//
// This is a v1 federation: each tab pulls from the right upstream provider
// client-side and converts to a uniform `InboxItem`. When the
// `notification-service` ships a single fan-out endpoint we'll collapse this
// into one server call.
//
// Conventions:
//   - `FutureProvider.autoDispose.family<List<InboxItem>, InboxFilter>`
//   - Each `InboxItem` is immutable; equality is identity-based, used only
//     to dedupe and key lists.

import 'package:atpost_app/data/models/notification.dart';
import 'package:atpost_app/data/models/order.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/providers/notification_provider.dart';
import 'package:atpost_app/providers/orders_provider.dart';
import 'package:atpost_app/providers/pulse_providers.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Filter values that drive the Inbox tab strip in the shell.
enum InboxFilter {
  all,
  mentions,
  pulse,
  commerce,
  system,
}

extension InboxFilterX on InboxFilter {
  /// String key — used as the `tab` prop in shell telemetry and as a stable
  /// route fragment (`/inbox?tab=pulse`).
  String get key {
    switch (this) {
      case InboxFilter.all:
        return 'all';
      case InboxFilter.mentions:
        return 'mentions';
      case InboxFilter.pulse:
        return 'pulse';
      case InboxFilter.commerce:
        return 'commerce';
      case InboxFilter.system:
        return 'system';
    }
  }

  String get label {
    switch (this) {
      case InboxFilter.all:
        return 'All';
      case InboxFilter.mentions:
        return 'Mentions';
      case InboxFilter.pulse:
        return 'Pulse';
      case InboxFilter.commerce:
        return 'Commerce';
      case InboxFilter.system:
        return 'System';
    }
  }
}

/// Source / "kind" of an inbox row. Drives the icon and the routing.
enum InboxKind {
  mention,
  follow,
  like,
  comment,
  pulseMatch,
  pulseSpark,
  pulseMessage,
  commerceOrderUpdate,
  commerceShipped,
  commerceDelivered,
  system,
  other,
}

class InboxItem {
  const InboxItem({
    required this.id,
    required this.kind,
    required this.title,
    required this.snippet,
    required this.time,
    required this.unread,
    this.deepLink,
    this.avatarUrl,
    this.iconName,
  });

  final String id;
  final InboxKind kind;
  final String title;
  final String snippet;
  final DateTime time;
  final bool unread;
  final String? deepLink;
  final String? avatarUrl;

  /// Stable string identifier for the icon (e.g. `notifications`,
  /// `favorite`). The shell maps this to `IconData` — keeps this provider
  /// free of `material.dart` imports.
  final String? iconName;
}

// ─── Mappers ───────────────────────────────────────────────────────────

InboxItem _fromAppNotification(AppNotification n) {
  // type is a free-form string from notification-service. Map the most
  // common ones to inbox kinds; everything else falls back to `other`.
  InboxKind kind;
  String iconName;
  switch (n.type) {
    case 'mention':
    case 'post_mention':
    case 'comment_mention':
      kind = InboxKind.mention;
      iconName = 'alternate_email';
      break;
    case 'follow':
    case 'new_follower':
      kind = InboxKind.follow;
      iconName = 'person_add';
      break;
    case 'like':
    case 'reaction':
      kind = InboxKind.like;
      iconName = 'favorite';
      break;
    case 'comment':
    case 'reply':
      kind = InboxKind.comment;
      iconName = 'mode_comment';
      break;
    case 'system':
    case 'announcement':
      kind = InboxKind.system;
      iconName = 'campaign';
      break;
    default:
      kind = InboxKind.other;
      iconName = 'notifications';
  }
  return InboxItem(
    id: n.id,
    kind: kind,
    title: _titleForNotification(n),
    snippet: _snippetForNotification(n),
    time: n.createdAt,
    unread: !n.isRead,
    deepLink: n.deepLink,
    iconName: iconName,
  );
}

String _titleForNotification(AppNotification n) {
  switch (n.type) {
    case 'mention':
    case 'post_mention':
      return 'New mention';
    case 'follow':
    case 'new_follower':
      return 'New follower';
    case 'like':
    case 'reaction':
      return 'New reaction';
    case 'comment':
    case 'reply':
      return 'New comment';
    case 'system':
    case 'announcement':
      return 'VChat';
    default:
      return 'Notification';
  }
}

String _snippetForNotification(AppNotification n) {
  // We don't have rich payload — fall back to the entity reference.
  if (n.entityType.isEmpty) return '';
  return '${n.entityType} ${n.entityId}'.trim();
}

InboxItem _fromMatchSummary(MatchSummary m) {
  final isSpark = m.status == 'sparks_waiting';
  return InboxItem(
    id: 'pulse-match-${m.id}',
    kind: isSpark ? InboxKind.pulseSpark : InboxKind.pulseMatch,
    title: isSpark ? 'New spark' : 'Match with ${m.otherFirstName}',
    snippet: m.lastMessagePreview ?? (isSpark ? 'Tap to see who' : 'Say hi'),
    time: m.lastMessageAt ?? DateTime.now(),
    unread: m.lastMessagePreview != null && m.lastMessagePreview!.isNotEmpty,
    avatarUrl: m.otherAvatarUrl,
    deepLink: m.conversationId != null
        ? '/pulse/chat/${m.conversationId}'
        : '/pulse/matches/${m.id}',
    iconName: isSpark ? 'flash_on' : 'favorite',
  );
}

InboxItem _fromOrder(Order o) {
  InboxKind kind;
  String title;
  switch (o.status) {
    case 'shipped':
      kind = InboxKind.commerceShipped;
      title = 'Order shipped';
      break;
    case 'delivered':
      kind = InboxKind.commerceDelivered;
      title = 'Order delivered';
      break;
    default:
      kind = InboxKind.commerceOrderUpdate;
      title = 'Order ${o.status}';
  }
  final firstItem =
      o.items.isNotEmpty ? o.items.first.productName : 'your order';
  return InboxItem(
    id: 'order-${o.id}',
    kind: kind,
    title: title,
    snippet: firstItem,
    time: o.createdAt,
    // Treat anything still-active as "unread" for badge purposes — the user
    // will mark it read by tapping into the order surface.
    unread: o.isActive,
    deepLink: '/commerce/orders/${o.id}',
    iconName: 'shopping_bag',
  );
}

// ─── Providers ─────────────────────────────────────────────────────────

/// Federate the upstream sources for a single inbox tab.
final unifiedInboxProvider = FutureProvider.autoDispose
    .family<List<InboxItem>, InboxFilter>((ref, filter) async {
  switch (filter) {
    case InboxFilter.all:
      final results = await Future.wait([
        _fetchNotifications(ref, onlyMentions: false),
        _fetchPulse(ref),
        _fetchCommerce(ref),
      ]);
      final merged = <InboxItem>[
        for (final list in results) ...list,
      ]..sort((a, b) => b.time.compareTo(a.time));
      return merged;

    case InboxFilter.mentions:
      return _fetchNotifications(ref, onlyMentions: true);

    case InboxFilter.pulse:
      return _fetchPulse(ref);

    case InboxFilter.commerce:
      return _fetchCommerce(ref);

    case InboxFilter.system:
      return _fetchSystem(ref);
  }
});

Future<List<InboxItem>> _fetchNotifications(
  Ref ref, {
  required bool onlyMentions,
}) async {
  try {
    final list = await ref.watch(notificationsProvider.future);
    final items = list.map(_fromAppNotification).toList(growable: false);
    if (!onlyMentions) return items;
    return items.where((i) => i.kind == InboxKind.mention).toList(
          growable: false,
        );
  } catch (_) {
    return const <InboxItem>[];
  }
}

Future<List<InboxItem>> _fetchPulse(Ref ref) async {
  try {
    final matches = await ref.watch(pulseMatchesProvider('all').future);
    return matches.map(_fromMatchSummary).toList(growable: false)
      ..sort((a, b) => b.time.compareTo(a.time));
  } catch (_) {
    // Pulse may be gated off — empty list is the right fallback.
    return const <InboxItem>[];
  }
}

Future<List<InboxItem>> _fetchCommerce(Ref ref) async {
  try {
    final orders = await ref.watch(ordersProvider.future);
    // Recent only — the inbox isn't a full order history.
    final cutoff = DateTime.now().subtract(const Duration(days: 30));
    return orders
        .where((o) => o.createdAt.isAfter(cutoff))
        .map(_fromOrder)
        .toList(growable: false)
      ..sort((a, b) => b.time.compareTo(a.time));
  } catch (_) {
    return const <InboxItem>[];
  }
}

Future<List<InboxItem>> _fetchSystem(Ref ref) async {
  try {
    final list = await ref.watch(notificationsProvider.future);
    return list
        .where((n) => n.type == 'system' || n.type == 'announcement')
        .map(_fromAppNotification)
        .toList(growable: false);
  } catch (_) {
    return const <InboxItem>[];
  }
}

/// Per-category unread counts for the tab badges. Returns 0 on failure so
/// the UI never throws on a transient miss.
final unreadCountByCategoryProvider = FutureProvider.autoDispose
    .family<int, InboxFilter>((ref, filter) async {
  final items = await ref.watch(unifiedInboxProvider(filter).future);
  return items.where((i) => i.unread).length;
});
