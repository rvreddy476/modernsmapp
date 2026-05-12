// Tests for the NotificationToastQueue collapse / summary logic.
//
// The queue is the only piece of client-side behavior between
// receiving a notification and showing a toast; getting collapse
// right per README §8 + §9 prevents the "5 notifications, only the
// last one shown" UX regression we were chasing.

import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/features/shell/notification_toast_queue.dart';
import 'package:flutter_test/flutter_test.dart';

NotificationEvent _event({
  required String collapseKey,
  String title = '',
  String body = '',
  String? deepLink,
}) {
  // Build the payload shape processor.go emits over the SSE wire so
  // the queue exercises the same parsing path the real app does.
  return NotificationEvent(
    payload: <String, dynamic>{
      'notification_id': 'n-${collapseKey.hashCode}-${title.hashCode}',
      'event_type': 'post.liked',
      'title': title,
      'body': body,
      'deep_link': deepLink,
      'collapse_key': collapseKey,
      'created_at': DateTime.now().toIso8601String(),
    },
  );
}

void main() {
  group('NotificationToastQueue', () {
    test('single event → toast title is the event title', () async {
      NotificationToastView? captured;
      final queue = NotificationToastQueue(onView: (v) => captured = v);
      queue.add(_event(
        collapseKey: 'like:post:1',
        title: 'Ravi liked your post',
        deepLink: '/post/1',
      ));

      // Wait past the queue's 200 ms debounce.
      await Future<void>.delayed(const Duration(milliseconds: 250));

      expect(captured, isNotNull);
      expect(captured!.title, 'Ravi liked your post');
      expect(captured!.eventCount, 1);
      expect(captured!.isSummary, isFalse);
      expect(captured!.deepLink, '/post/1');
      queue.dispose();
    });

    test('multiple events same collapse_key → "+N more" body', () async {
      NotificationToastView? captured;
      final queue = NotificationToastQueue(onView: (v) => captured = v);
      queue.add(_event(collapseKey: 'like:post:1', title: 'Ravi liked your post'));
      queue.add(_event(collapseKey: 'like:post:1', title: 'Suresh liked your post'));
      queue.add(_event(collapseKey: 'like:post:1', title: 'Naresh liked your post'));

      await Future<void>.delayed(const Duration(milliseconds: 250));

      expect(captured, isNotNull);
      expect(captured!.title, 'Naresh liked your post');
      expect(captured!.body, '+2 more');
      expect(captured!.eventCount, 3);
      expect(captured!.isSummary, isFalse);
      queue.dispose();
    });

    test('three+ distinct collapse_keys → summary toast', () async {
      NotificationToastView? captured;
      final queue = NotificationToastQueue(onView: (v) => captured = v);
      queue.add(_event(collapseKey: 'like:post:1', title: 'A liked your post'));
      queue.add(_event(collapseKey: 'comment:post:1', title: 'B commented'));
      queue.add(_event(collapseKey: 'follow:user:1', title: 'C followed you'));

      await Future<void>.delayed(const Duration(milliseconds: 250));

      expect(captured, isNotNull);
      expect(captured!.isSummary, isTrue);
      expect(captured!.title, contains('3 new notifications'));
      // Summary always routes to the inbox, not a single deep link.
      expect(captured!.deepLink, '/notifications');
      expect(captured!.eventCount, 3);
      queue.dispose();
    });

    test('two distinct collapse_keys → latest + "+N more"', () async {
      NotificationToastView? captured;
      final queue = NotificationToastQueue(onView: (v) => captured = v);
      queue.add(_event(collapseKey: 'like:post:1', title: 'First'));
      queue.add(_event(collapseKey: 'comment:post:1', title: 'Second'));

      await Future<void>.delayed(const Duration(milliseconds: 250));

      expect(captured, isNotNull);
      expect(captured!.title, 'Second'); // latest wins
      expect(captured!.body, '+1 more');
      expect(captured!.isSummary, isFalse);
      queue.dispose();
    });

    test('empty collapse_key gets a synthetic standalone key — does NOT collapse', () async {
      NotificationToastView? captured;
      final queue = NotificationToastQueue(onView: (v) => captured = v);
      // Two mention events (collapse_key intentionally empty in
      // push_collapse.go for mentions). They must each be treated as
      // distinct groups so neither swallows the other.
      queue.add(_event(collapseKey: '', title: '@you was mentioned'));
      queue.add(_event(collapseKey: '', title: '@you was mentioned again'));

      await Future<void>.delayed(const Duration(milliseconds: 250));

      expect(captured, isNotNull);
      // 2 distinct synthetic keys → mode 3 ("+1 more"), not collapsed.
      expect(captured!.eventCount, 2);
      expect(captured!.title, '@you was mentioned again');
      queue.dispose();
    });

    test('only the latest burst is rendered (debounce resets)', () async {
      final calls = <NotificationToastView>[];
      final queue = NotificationToastQueue(onView: calls.add);
      queue.add(_event(collapseKey: 'a', title: 'A'));
      await Future<void>.delayed(const Duration(milliseconds: 100));
      // Second add resets the 200ms timer — total wait should be
      // ~300ms before flush.
      queue.add(_event(collapseKey: 'a', title: 'A2'));
      await Future<void>.delayed(const Duration(milliseconds: 100));
      expect(calls, isEmpty); // not yet flushed
      await Future<void>.delayed(const Duration(milliseconds: 150));
      expect(calls.length, 1);
      expect(calls.first.title, 'A2');
      queue.dispose();
    });

    test('disposing stops pending flush', () async {
      var fired = 0;
      final queue = NotificationToastQueue(onView: (_) => fired++);
      queue.add(_event(collapseKey: 'a', title: 'X'));
      queue.dispose();
      await Future<void>.delayed(const Duration(milliseconds: 250));
      expect(fired, 0);
    });
  });
}
