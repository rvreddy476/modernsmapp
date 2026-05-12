// Notification toast queue with collapse-by-key + burst summary.
//
// Why this exists: the shell used to call `removeCurrentSnackBar`
// before showing every new notification, so a burst of 5 in quick
// succession surfaced as just the last one — README §8 calls this
// out specifically as a bad UX pattern.
//
// Behavior:
//   - 1 event in the burst window → show its title verbatim.
//   - 2+ events sharing a collapse_key → show the latest title with a
//     "+N more" body (the backend's `notification_update` aggregation
//     already handles the "Ravi and 3 others …" form for the same
//     entity; this client-side path catches the case where multiple
//     pushes raced past aggregation).
//   - 2+ distinct collapse_keys → show the latest title plus a
//     "+N more" hint.
//   - At/above _summaryThreshold distinct keys → switch to summary
//     mode: "N new notifications", deep-link to the notification
//     center.
//
// Implementation notes:
//   - Pure debounce + collapse. No persistent state; queue empties
//     after every flush.
//   - Caller owns the SnackBar widget — this controller only computes
//     the *view* (title/body/icon/deepLink) and hands it back via a
//     callback. Keeps theme + ScaffoldMessenger references in the
//     shell file, controller stays Flutter-framework-agnostic.

import 'dart:async';

import 'package:atpost_app/data/models/realtime_event.dart';

/// Immutable view-model the shell renders into a single SnackBar.
class NotificationToastView {
  const NotificationToastView({
    required this.title,
    required this.body,
    required this.deepLink,
    required this.eventCount,
    required this.isSummary,
  });

  /// Headline copy. Always non-empty.
  final String title;

  /// Sub-line copy (empty allowed).
  final String body;

  /// Validated in-app path. May be null when the burst spans multiple
  /// targets and we can't pick one safely; caller still shows the
  /// toast without an action.
  final String? deepLink;

  /// How many distinct real events were folded into this view —
  /// drives unread-counter invalidation cadence at the call site.
  final int eventCount;

  /// True when the view collapsed into "N new notifications" mode.
  /// The caller may want to route the action to /notifications
  /// rather than a single target's deep link.
  final bool isSummary;
}

class NotificationToastQueue {
  NotificationToastQueue({required this.onView});

  /// Invoked once per debounced burst with the merged view.
  final void Function(NotificationToastView view) onView;

  // Tuned for "feels live" without flickering on rapid bursts. 200 ms
  // is short enough that a single notification still feels instant;
  // any subsequent event inside that window collapses into the same
  // view.
  static const _debounce = Duration(milliseconds: 200);

  // Events older than this fall out of the window — defensive only;
  // _flush() always empties the buffer immediately after rendering.
  static const _windowSize = Duration(seconds: 8);

  // 3 distinct collapse keys is the threshold for summary mode. Two
  // we still show as "<latest> +1 more" so the user sees actual
  // content; three+ becomes "N new notifications".
  static const _summaryThreshold = 3;

  final List<NotificationEvent> _buffer = [];
  Timer? _timer;

  void add(NotificationEvent event) {
    _prune();
    _buffer.add(event);
    _timer?.cancel();
    _timer = Timer(_debounce, _flush);
  }

  void dispose() {
    _timer?.cancel();
    _buffer.clear();
  }

  void _prune() {
    final cutoff = DateTime.now().subtract(_windowSize);
    _buffer.removeWhere((e) => e.createdAt.isBefore(cutoff));
  }

  void _flush() {
    _prune();
    if (_buffer.isEmpty) return;

    // Group by collapse_key. Events without a key (mentions, urgent
    // alerts, security alerts) each get their own bucket — they're
    // designed to never collapse — but a unique synthetic key keeps
    // the data structure uniform.
    final groups = <String, List<NotificationEvent>>{};
    for (final e in _buffer) {
      final key = e.collapseKey.isEmpty
          ? 'standalone:${e.notificationId.isNotEmpty ? e.notificationId : e.ts}'
          : e.collapseKey;
      groups.putIfAbsent(key, () => []).add(e);
    }

    final view = _buildView(groups);
    _buffer.clear();
    onView(view);
  }

  NotificationToastView _buildView(
    Map<String, List<NotificationEvent>> groups,
  ) {
    final allEvents = groups.values.expand((g) => g).toList();
    final latest = allEvents.last;

    // ── Mode 1: one group total ──
    if (groups.length == 1) {
      final events = groups.values.first;
      if (events.length == 1) {
        // Single notification — show its title verbatim.
        return NotificationToastView(
          title: latest.title.isEmpty ? 'New notification' : latest.title,
          body: latest.body,
          deepLink: latest.deepLink,
          eventCount: 1,
          isSummary: false,
        );
      }
      // Same collapse key, multiple events — backend usually merges
      // these but if pushes race past aggregation we collapse here.
      return NotificationToastView(
        title: latest.title.isEmpty ? 'New activity' : latest.title,
        body: '+${events.length - 1} more',
        deepLink: latest.deepLink,
        eventCount: events.length,
        isSummary: false,
      );
    }

    // ── Mode 2: many distinct events → summary toast ──
    if (groups.length >= _summaryThreshold) {
      return NotificationToastView(
        title: '${allEvents.length} new notifications',
        body: 'Tap to view',
        deepLink: '/notifications',
        eventCount: allEvents.length,
        isSummary: true,
      );
    }

    // ── Mode 3: 2 distinct groups → latest + "+N more" hint ──
    return NotificationToastView(
      title: latest.title.isEmpty ? 'New notification' : latest.title,
      body: '+${allEvents.length - 1} more',
      deepLink: latest.deepLink,
      eventCount: allEvents.length,
      isSummary: false,
    );
  }
}
