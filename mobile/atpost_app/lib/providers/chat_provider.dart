import 'dart:async';

import 'package:atpost_app/data/models/conversation.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/realtime_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final chatConversationsProvider =
    FutureProvider.autoDispose<List<Conversation>>((ref) async {
      final auth = ref.watch(authServiceProvider);
      // Wait for session to be restored before making API calls
      await auth.sessionReady;

      if (!auth.isAuthenticated) {
        throw Exception('User not authenticated');
      }

      final repo = ref.watch(chatRepositoryProvider);
      return repo.getConversations();
    });

final chatConversationProvider = FutureProvider.autoDispose
    .family<Conversation, String>((ref, conversationId) async {
      final conversations = await ref.watch(chatConversationsProvider.future);
      for (final conversation in conversations) {
        if (conversation.id == conversationId) {
          return conversation;
        }
      }
      final repo = ref.watch(chatRepositoryProvider);
      return repo.getConversation(conversationId);
    });

final filteredConversationsProvider =
    Provider.autoDispose<AsyncValue<List<Conversation>>>((ref) {
      final conversationsAsync = ref.watch(chatConversationsProvider);
      final query = ref.watch(chatSearchQueryProvider).toLowerCase();
      final activeFilter = ref.watch(chatActiveFilterProvider);
      final currentUserId = ref.watch(authServiceProvider).userId;

      return conversationsAsync.whenData((list) {
        return list.where((conversation) {
          final matchesFilter = switch (activeFilter) {
            0 => !conversation.isArchived, // All (active)
            1 => conversation.unreadCount > 0 && !conversation.isArchived, // Unread
            2 => conversation.type == 'group' && !conversation.isArchived, // Groups
            3 => conversation.isArchived, // Archived
            _ => !conversation.isArchived,
          };
          if (!matchesFilter) return false;

          if (query.isEmpty) return true;
          final name = conversation.displayNameFor(currentUserId).toLowerCase();
          final lastMessage = (conversation.lastMessage ?? '').toLowerCase();
          return name.contains(query) || lastMessage.contains(query);
        }).toList();
      });
    });

final chatSearchQueryProvider = StateProvider<String>((ref) => '');
final chatActiveFilterProvider = StateProvider<int>((ref) => 0);

class ChatMessagesState {
  final List<Message> messages;
  final bool isLoading;
  final bool isLoadingOlder;
  final bool isSending;
  final String? error;
  final bool hasReachedEnd;
  final String? nextCursor;
  final Set<String> typingUserIds;
  final Map<String, DateTime> readReceipts;

  const ChatMessagesState({
    this.messages = const [],
    this.isLoading = false,
    this.isLoadingOlder = false,
    this.isSending = false,
    this.error,
    this.hasReachedEnd = false,
    this.nextCursor,
    this.typingUserIds = const <String>{},
    this.readReceipts = const <String, DateTime>{},
  });

  ChatMessagesState copyWith({
    List<Message>? messages,
    bool? isLoading,
    bool? isLoadingOlder,
    bool? isSending,
    String? error,
    bool? hasReachedEnd,
    String? nextCursor,
    Set<String>? typingUserIds,
    Map<String, DateTime>? readReceipts,
  }) {
    return ChatMessagesState(
      messages: messages ?? this.messages,
      isLoading: isLoading ?? this.isLoading,
      isLoadingOlder: isLoadingOlder ?? this.isLoadingOlder,
      isSending: isSending ?? this.isSending,
      error: error,
      hasReachedEnd: hasReachedEnd ?? this.hasReachedEnd,
      nextCursor: nextCursor ?? this.nextCursor,
      typingUserIds: typingUserIds ?? this.typingUserIds,
      readReceipts: readReceipts ?? this.readReceipts,
    );
  }
}

class ChatMessagesNotifier extends StateNotifier<ChatMessagesState> {
  final ChatRepository _repo;
  final RealtimeService _realtime;
  final String? _currentUserId;
  final String conversationId;

  StreamSubscription? _realtimeSub;
  Timer? _typingDebounce;
  final Map<String, Timer> _typingExpiryTimers = <String, Timer>{};

  ChatMessagesNotifier(
    this._repo,
    this._realtime,
    this._currentUserId,
    this.conversationId,
  ) : super(const ChatMessagesState()) {
    _init();
  }

  Future<void> _init() async {
    await loadMessages();
    _listenToRealtime();
  }

  Future<void> loadMessages() async {
    if (state.isLoading) return;

    state = state.copyWith(isLoading: true, error: null);
    try {
      final page = await _repo.getMessages(conversationId);
      final sorted = [...page.messages]
        ..sort((a, b) => a.createdAt.compareTo(b.createdAt));

      state = state.copyWith(
        messages: sorted,
        isLoading: false,
        nextCursor: page.nextCursor,
        hasReachedEnd: page.nextCursor == null || page.nextCursor!.isEmpty,
      );
    } catch (_) {
      state = state.copyWith(
        isLoading: false,
        error: 'Failed to load messages',
      );
      return;
    }

    try {
      await _markLatestIncomingMessageRead();
    } catch (_) {
      // Keep the loaded message state even if read-receipt persistence fails.
    }
  }

  Future<void> loadOlderMessages() async {
    if (state.isLoadingOlder ||
        state.hasReachedEnd ||
        state.nextCursor == null) {
      return;
    }

    state = state.copyWith(isLoadingOlder: true, error: null);
    try {
      final page = await _repo.getMessages(
        conversationId,
        cursor: state.nextCursor,
      );
      final existingIds = state.messages.map((message) => message.id).toSet();
      final older = page.messages
          .where((message) => !existingIds.contains(message.id))
          .toList();
      final messages = [...older, ...state.messages]
        ..sort((a, b) => a.createdAt.compareTo(b.createdAt));

      state = state.copyWith(
        messages: messages,
        nextCursor: page.nextCursor,
        hasReachedEnd: page.nextCursor == null || page.nextCursor!.isEmpty,
        isLoadingOlder: false,
      );
    } catch (_) {
      state = state.copyWith(
        isLoadingOlder: false,
        error: 'Failed to load older messages',
      );
    }
  }

  Future<void> sendMessage(String content) async {
    if (content.trim().isEmpty) return;

    state = state.copyWith(isSending: true, error: null);
    try {
      final message = await _repo.sendMessage(conversationId, content.trim());
      final messages = [...state.messages, message]
        ..sort((a, b) => a.createdAt.compareTo(b.createdAt));

      state = state.copyWith(messages: messages, isSending: false);
    } catch (_) {
      state = state.copyWith(isSending: false, error: 'Failed to send message');
    }
  }

  void onComposerChanged(String value) {
    if (value.trim().isEmpty) {
      _typingDebounce?.cancel();
      return;
    }

    _typingDebounce?.cancel();
    _typingDebounce = Timer(const Duration(milliseconds: 350), () {
      unawaited(_repo.sendTyping(conversationId));
    });
  }

  void _listenToRealtime() {
    _realtimeSub?.cancel();
    _realtimeSub = _realtime.events.listen((event) {
      if (event is ChatMessageEvent &&
          event.conversationId == conversationId &&
          event.senderId != _currentUserId) {
        final message = Message.fromJson({
          'conversation_id': event.conversationId,
          'message_id': event.messageId,
          'sender_id': event.senderId,
          'type': event.messageType,
          'text': event.text,
          'media_id': event.mediaId,
          'created_at': event.createdAt.toIso8601String(),
        });

        final exists = state.messages.any((item) => item.id == message.id);
        if (!exists) {
          final messages = [...state.messages, message]
            ..sort((a, b) => a.createdAt.compareTo(b.createdAt));
          state = state.copyWith(messages: messages);
          unawaited(_repo.markRead(conversationId, message.id));
        }
        return;
      }

      if (event is TypingEvent && event.conversationId == conversationId) {
        final next = {...state.typingUserIds};
        if (event.isTyping) {
          next.add(event.userId);
          _typingExpiryTimers[event.userId]?.cancel();
          _typingExpiryTimers[event.userId] = Timer(
            const Duration(seconds: 4),
            () {
              final updated = {...state.typingUserIds}..remove(event.userId);
              state = state.copyWith(typingUserIds: updated);
              _typingExpiryTimers.remove(event.userId);
            },
          );
        } else {
          next.remove(event.userId);
          _typingExpiryTimers.remove(event.userId)?.cancel();
        }
        state = state.copyWith(typingUserIds: next);
        return;
      }

      if (event is ReadReceiptEvent && event.conversationId == conversationId) {
        final receipts = {...state.readReceipts, event.userId: event.readAt};
        state = state.copyWith(readReceipts: receipts);
      }
    });
  }

  Future<void> _markLatestIncomingMessageRead() async {
    final messages = state.messages;
    for (final message in messages.reversed) {
      if (message.senderId != _currentUserId) {
        await _repo.markRead(conversationId, message.id);
        break;
      }
    }
  }

  @override
  void dispose() {
    _typingDebounce?.cancel();
    for (final timer in _typingExpiryTimers.values) {
      timer.cancel();
    }
    _typingExpiryTimers.clear();
    _realtimeSub?.cancel();
    super.dispose();
  }
}

final chatMessagesProvider = StateNotifierProvider.autoDispose
    .family<ChatMessagesNotifier, ChatMessagesState, String>((
      ref,
      conversationId,
    ) {
      final repo = ref.watch(chatRepositoryProvider);
      final realtime = ref.watch(realtimeServiceProvider);
      final currentUserId = ref.watch(authServiceProvider).userId;
      return ChatMessagesNotifier(
        repo,
        realtime,
        currentUserId,
        conversationId,
      );
    });

class PeerPresenceNotifier extends StateNotifier<AsyncValue<bool>> {
  final ChatRepository _repo;
  final RealtimeService _realtime;
  final String userId;

  StreamSubscription? _realtimeSub;
  bool _receivedRealtimeUpdate = false;

  PeerPresenceNotifier(this._repo, this._realtime, this.userId)
    : super(const AsyncValue.loading()) {
    _load();
    _realtimeSub = _realtime.events.listen((event) {
      if (event is PresenceUpdateEvent && event.userId == userId) {
        _receivedRealtimeUpdate = true;
        state = AsyncValue.data(event.isOnline);
      }
    });
  }

  Future<void> _load() async {
    try {
      final presence = await _repo.getPresence([userId]);
      if (_receivedRealtimeUpdate) {
        return;
      }
      state = AsyncValue.data(presence[userId] ?? false);
    } catch (error, stackTrace) {
      if (_receivedRealtimeUpdate) {
        return;
      }
      state = AsyncValue.error(error, stackTrace);
    }
  }

  @override
  void dispose() {
    _realtimeSub?.cancel();
    super.dispose();
  }
}

final peerPresenceProvider = StateNotifierProvider.autoDispose
    .family<PeerPresenceNotifier, AsyncValue<bool>, String>((ref, userId) {
      final repo = ref.watch(chatRepositoryProvider);
      final realtime = ref.watch(realtimeServiceProvider);
      return PeerPresenceNotifier(repo, realtime, userId);
    });
