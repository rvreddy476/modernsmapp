import 'dart:async';

import 'package:atpost_app/data/models/conversation.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:atpost_app/providers/chat_provider.dart';
import 'package:fake_async/fake_async.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import '../helpers/mocks.dart';

Message _message({
  required String id,
  required String senderId,
  required DateTime createdAt,
  String conversationId = 'conv-1',
  String content = 'hello',
}) {
  return Message(
    id: id,
    conversationId: conversationId,
    senderId: senderId,
    content: content,
    createdAt: createdAt,
  );
}

Future<void> _settleNotifier() async {
  await Future<void>.delayed(Duration.zero);
  await Future<void>.delayed(Duration.zero);
}

bool _presenceValue(PeerPresenceNotifier notifier) {
  final value = notifier.state.value;
  expect(value, isNotNull);
  return value!;
}

void main() {
  late MockChatRepository repo;
  late MockRealtimeService realtime;
  late StreamController<RealtimeEvent> controller;

  setUp(() {
    repo = MockChatRepository();
    realtime = MockRealtimeService();
    controller = StreamController<RealtimeEvent>.broadcast();

    when(() => realtime.events).thenAnswer((_) => controller.stream);
    when(
      () => repo.getMessages(
        any(),
        limit: any(named: 'limit'),
        cursor: any(named: 'cursor'),
      ),
    ).thenAnswer((_) async => const ChatPage(messages: []));
    when(() => repo.markRead(any(), any())).thenAnswer((_) async {});
    when(() => repo.sendTyping(any())).thenAnswer((_) async {});
    when(() => repo.getPresence(any())).thenAnswer((_) async => const {});

    addTearDown(() async {
      await controller.close();
    });
  });

  group('ChatMessagesNotifier', () {
    test(
      'keeps loaded messages when markRead fails during initial load',
      () async {
        when(
          () => repo.getMessages(
            'conv-1',
            limit: any(named: 'limit'),
            cursor: any(named: 'cursor'),
          ),
        ).thenAnswer(
          (_) async => ChatPage(
            messages: [
              _message(
                id: 'msg-2',
                senderId: 'user-2',
                createdAt: DateTime.parse('2026-04-11T10:02:00Z'),
              ),
              _message(
                id: 'msg-1',
                senderId: 'me',
                createdAt: DateTime.parse('2026-04-11T10:01:00Z'),
              ),
            ],
          ),
        );
        when(
          () => repo.markRead('conv-1', 'msg-2'),
        ).thenThrow(Exception('mark read failed'));

        final notifier = ChatMessagesNotifier(repo, realtime, 'me', 'conv-1');
        addTearDown(notifier.dispose);

        await _settleNotifier();

        expect(notifier.state.error, isNull);
        expect(notifier.state.isLoading, isFalse);
        expect(notifier.state.messages.map((message) => message.id), [
          'msg-1',
          'msg-2',
        ]);
        verify(() => repo.markRead('conv-1', 'msg-2')).called(1);
      },
    );

    test('appends an unseen realtime message once and marks it read', () async {
      final notifier = ChatMessagesNotifier(repo, realtime, 'me', 'conv-1');
      addTearDown(notifier.dispose);

      await _settleNotifier();

      final event = ChatMessageEvent(
        payload: {
          'conversation_id': 'conv-1',
          'message_id': 'msg-1',
          'sender_id': 'user-2',
          'type': 'text',
          'text': 'hey',
          'created_at': '2026-04-11T10:03:00Z',
        },
      );

      controller.add(event);
      await _settleNotifier();
      controller.add(event);
      await _settleNotifier();

      expect(notifier.state.messages.map((message) => message.id), ['msg-1']);
      verify(() => repo.markRead('conv-1', 'msg-1')).called(1);
    });

    test('debounces typing notifications from composer changes', () {
      fakeAsync((async) {
        final notifier = ChatMessagesNotifier(repo, realtime, 'me', 'conv-1');
        addTearDown(notifier.dispose);

        async.flushMicrotasks();
        clearInteractions(repo);

        // First keystroke sends the leading typing ping immediately
        notifier.onComposerChanged('hel');
        verify(() => repo.sendTyping('conv-1')).called(1);
        clearInteractions(repo);

        // Subsequent keystrokes within 2 seconds (throttle) do not send another ping
        notifier.onComposerChanged('hello');
        async.elapse(const Duration(seconds: 1));
        verifyNever(() => repo.sendTyping(any()));

        // Keystroke after the idle reset interval sends another ping
        async.elapse(const Duration(seconds: 3));
        notifier.onComposerChanged('hello world');
        verify(() => repo.sendTyping('conv-1')).called(1);
        clearInteractions(repo);

        // Empty composer resets/cancels the typing state and doesn't send pings
        notifier.onComposerChanged('   ');
        async.elapse(const Duration(seconds: 3));
        verifyNever(() => repo.sendTyping(any()));
      });
    });

    test(
      'expires typing indicators and records read receipts from realtime',
      () {
        fakeAsync((async) {
          final notifier = ChatMessagesNotifier(repo, realtime, 'me', 'conv-1');
          addTearDown(notifier.dispose);

          async.flushMicrotasks();

          controller.add(
            TypingEvent(
              payload: {
                'conversation_id': 'conv-1',
                'user_id': 'user-2',
                'is_typing': true,
              },
            ),
          );
          controller.add(
            ReadReceiptEvent(
              payload: {
                'conversation_id': 'conv-1',
                'user_id': 'user-2',
                'message_id': 'msg-1',
                'read_at': '2026-04-11T10:04:00Z',
              },
            ),
          );
          async.flushMicrotasks();

          expect(notifier.state.typingUserIds, {'user-2'});
          expect(
            notifier.state.readReceipts['user-2'],
            DateTime.parse('2026-04-11T10:04:00Z'),
          );

          async.elapse(const Duration(seconds: 4));
          async.flushMicrotasks();

          expect(notifier.state.typingUserIds, isEmpty);
        });
      },
    );
  });

  group('PeerPresenceNotifier', () {
    test(
      'does not let a stale initial fetch overwrite a newer realtime update',
      () async {
        final completer = Completer<Map<String, bool>>();
        when(
          () => repo.getPresence(['user-2']),
        ).thenAnswer((_) => completer.future);

        final notifier = PeerPresenceNotifier(repo, realtime, 'user-2');
        addTearDown(notifier.dispose);

        controller.add(
          PresenceUpdateEvent(payload: {'user_id': 'user-2', 'online': true}),
        );
        await _settleNotifier();

        completer.complete({'user-2': false});
        await _settleNotifier();

        expect(_presenceValue(notifier), isTrue);
      },
    );

    test(
      'loads initial presence when no realtime update arrives first',
      () async {
        when(
          () => repo.getPresence(['user-2']),
        ).thenAnswer((_) async => {'user-2': true});

        final notifier = PeerPresenceNotifier(repo, realtime, 'user-2');
        addTearDown(notifier.dispose);

        await _settleNotifier();

        expect(_presenceValue(notifier), isTrue);
      },
    );
  });
}
