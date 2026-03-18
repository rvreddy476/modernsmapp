import 'dart:async';

import 'package:atpost_app/data/models/conversation.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:atpost_app/services/realtime_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Provider for the list of conversations.
final chatConversationsProvider =
    FutureProvider.autoDispose<List<Conversation>>((ref) async {
  final repo = ref.watch(chatRepositoryProvider);
  return repo.getConversations();
});

/// Per-conversation messages state.
class ChatMessagesState {
  final List<Message> messages;
  final bool isLoading;
  final bool isSending;
  final String? error;

  const ChatMessagesState({
    this.messages = const [],
    this.isLoading = false,
    this.isSending = false,
    this.error,
  });

  ChatMessagesState copyWith({
    List<Message>? messages,
    bool? isLoading,
    bool? isSending,
    String? error,
  }) {
    return ChatMessagesState(
      messages: messages ?? this.messages,
      isLoading: isLoading ?? this.isLoading,
      isSending: isSending ?? this.isSending,
      error: error,
    );
  }
}

/// Notifier for per-conversation messages with real-time updates.
class ChatMessagesNotifier extends StateNotifier<ChatMessagesState> {
  final ChatRepository _repo;
  final RealtimeService _realtime;
  final String conversationId;
  StreamSubscription? _realtimeSub;

  ChatMessagesNotifier(this._repo, this._realtime, this.conversationId)
      : super(const ChatMessagesState()) {
    _init();
  }

  Future<void> _init() async {
    await loadMessages();
    _listenToRealtime();
  }

  Future<void> loadMessages() async {
    state = state.copyWith(isLoading: true, error: null);
    try {
      final messages = await _repo.getMessages(conversationId);
      state = state.copyWith(messages: messages, isLoading: false);
    } catch (e) {
      state = state.copyWith(
        isLoading: false,
        error: 'Failed to load messages',
      );
    }
  }

  Future<void> sendMessage(String content) async {
    if (content.trim().isEmpty) return;
    state = state.copyWith(isSending: true);
    try {
      final message = await _repo.sendMessage(conversationId, content);
      state = state.copyWith(
        messages: [...state.messages, message],
        isSending: false,
      );
    } catch (e) {
      state = state.copyWith(
        isSending: false,
        error: 'Failed to send message',
      );
    }
  }

  void _listenToRealtime() {
    _realtimeSub?.cancel();
    _realtimeSub = _realtime.events.listen((event) {
      if (event is ChatMessageEvent) {
        final payload = event.payload;
        if (payload is Map<String, dynamic>) {
          final eventConvId = payload['conversation_id'] as String?;
          if (eventConvId == conversationId) {
            final message = Message.fromJson(payload);
            // Avoid duplicates
            final exists = state.messages.any((m) => m.id == message.id);
            if (!exists) {
              state = state.copyWith(
                messages: [...state.messages, message],
              );
            }
          }
        }
      }
    });
  }

  @override
  void dispose() {
    _realtimeSub?.cancel();
    super.dispose();
  }
}

/// Provider for per-conversation messages with real-time updates.
final chatMessagesProvider = StateNotifierProvider.autoDispose
    .family<ChatMessagesNotifier, ChatMessagesState, String>(
  (ref, conversationId) {
    final repo = ref.watch(chatRepositoryProvider);
    final realtime = ref.watch(realtimeServiceProvider);
    return ChatMessagesNotifier(repo, realtime, conversationId);
  },
);
