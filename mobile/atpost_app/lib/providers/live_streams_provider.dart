// Riverpod providers for the live-streaming-v2 surface. The legacy
// `live_repository.dart` providers stay in place for the v1 screens —
// these new providers serve the LiveKit-backed v2 screens
// (live_list_screen, live_viewer_screen, go_live_screen,
// live_broadcaster_screen) and use the v2 repo + models.

import 'dart:async';

import 'package:atpost_app/data/models/live_stream_v2.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/data/repositories/live_streams_repository.dart';
import 'package:atpost_app/services/realtime_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Current snapshot of the live-now grid (first page).
/// The viewer screens pull the live stream detail via
/// [liveStreamDetailProvider] keyed by stream id.
final liveStreamsListProvider =
    FutureProvider.autoDispose<LiveStreamPage>((ref) async {
  final repo = ref.watch(liveStreamsRepositoryProvider);
  return repo.listLive(limit: 24);
});

/// Per-stream detail. autoDispose so polling stops when the viewer
/// navigates away. Callers ref.refresh() this provider every few
/// seconds to update the viewer-peak field.
final liveStreamDetailProvider =
    FutureProvider.autoDispose.family<LiveStreamV2, String>((ref, streamId) async {
  final repo = ref.watch(liveStreamsRepositoryProvider);
  return repo.getStream(streamId);
});

/// Issues a subscriber LiveKit token for the viewer flow. Backend
/// applies the visibility gate (public/followers/paid).
final liveViewerTokenProvider =
    FutureProvider.autoDispose.family<ViewerTokenResult, String>((ref, streamId) async {
  final repo = ref.watch(liveStreamsRepositoryProvider);
  return repo.getViewerToken(streamId);
});

/// Broadcaster start/end mutations. We expose them as plain async
/// methods exposed through the repository — there's no shared mutable
/// state worth holding inside an AsyncNotifier, and the screens already
/// own their own connection lifecycle via the LiveKit Room handle.
class LiveBroadcasterController {
  final Ref _ref;
  LiveBroadcasterController(this._ref);

  Future<StartLiveStreamResult> start(String streamId) async {
    final repo = _ref.read(liveStreamsRepositoryProvider);
    return repo.startStream(streamId);
  }

  Future<void> end(String streamId) async {
    final repo = _ref.read(liveStreamsRepositoryProvider);
    return repo.endStream(streamId);
  }

  Future<LiveStreamV2> create({
    required String title,
    String description = '',
    String visibility = 'public',
    String? coverMediaId,
    DateTime? scheduledAt,
  }) async {
    final repo = _ref.read(liveStreamsRepositoryProvider);
    return repo.createStream(
      title: title,
      description: description,
      visibility: visibility,
      coverMediaId: coverMediaId,
      scheduledAt: scheduledAt,
    );
  }
}

final liveBroadcasterControllerProvider =
    Provider<LiveBroadcasterController>((ref) => LiveBroadcasterController(ref));

// ── Chat overlay (Phase A) ────────────────────────────────────────────
//
// State notifier per live stream. Loads the replay buffer on construct,
// subscribes to the ws-gateway `live:stream:{id}` pub/sub channel via
// RealtimeService, and exposes a send() method that hits the REST
// append + relies on the pub/sub echo for the broadcaster's own
// message to render (with id-dedup).

/// Internal state shape: oldest-first list so the UI can just append.
class LiveChatState {
  final List<LiveChatMessage> messages;
  final bool loaded;
  final String? errorMessage;

  const LiveChatState({
    required this.messages,
    required this.loaded,
    this.errorMessage,
  });

  static const empty = LiveChatState(messages: <LiveChatMessage>[], loaded: false);

  LiveChatState copyWith({
    List<LiveChatMessage>? messages,
    bool? loaded,
    String? errorMessage,
  }) {
    return LiveChatState(
      messages: messages ?? this.messages,
      loaded: loaded ?? this.loaded,
      errorMessage: errorMessage,
    );
  }
}

class LiveChatNotifier extends StateNotifier<LiveChatState> {
  LiveChatNotifier(this._repo, this._realtime, this.streamId)
      : super(LiveChatState.empty) {
    _bootstrap();
  }

  final LiveStreamsRepository _repo;
  final RealtimeService _realtime;
  final String streamId;
  StreamSubscription<RealtimeEvent>? _sub;

  Future<void> _bootstrap() async {
    // 1. Subscribe to the live tail. RealtimeService restores the
    //    subscription set on reconnect so we don't need to retry.
    _realtime.subscribeToLiveStream(streamId);
    _sub = _realtime.events.listen(_onEvent);

    // 2. Load the REST replay buffer (newest-first from backend; we
    //    flip to oldest-first locally).
    try {
      final initial = await _repo.listChat(streamId, limit: 50);
      // Backend returns newest-first.
      final oldestFirst = initial.reversed.toList(growable: true);
      if (!mounted) return;
      state = state.copyWith(messages: oldestFirst, loaded: true);
    } catch (e) {
      if (!mounted) return;
      state = state.copyWith(loaded: true, errorMessage: 'Couldn\'t load chat history.');
    }
  }

  void _onEvent(RealtimeEvent event) {
    if (event is! LiveChatMessageEvent) return;
    final payload = event.payload;
    if (payload is! Map<String, dynamic>) return;
    if (payload['stream_id'] != streamId) return;
    final msg = LiveChatMessage.fromJson(payload);
    _appendDedup(msg);
  }

  void _appendDedup(LiveChatMessage msg) {
    final list = state.messages;
    if (list.any((m) => m.id == msg.id)) return;
    state = state.copyWith(messages: [...list, msg], errorMessage: null);
  }

  Future<bool> send(String text) async {
    final trimmed = text.trim();
    if (trimmed.isEmpty) return false;
    try {
      final msg = await _repo.sendChat(streamId, trimmed);
      _appendDedup(msg);
      return true;
    } catch (e) {
      state = state.copyWith(errorMessage: _humanizeSendError(e));
      return false;
    }
  }

  String _humanizeSendError(Object e) {
    final s = e.toString().toLowerCase();
    if (s.contains('429') || s.contains('rate')) {
      return "You're sending too quickly. Wait a minute.";
    }
    if (s.contains('400') || s.contains('invalid')) {
      return 'Message rejected by the server.';
    }
    return 'Send failed. Try again.';
  }

  @override
  void dispose() {
    _sub?.cancel();
    _realtime.unsubscribeFromLiveStream(streamId);
    super.dispose();
  }
}

final liveChatProvider = StateNotifierProvider.autoDispose
    .family<LiveChatNotifier, LiveChatState, String>((ref, streamId) {
  final repo = ref.watch(liveStreamsRepositoryProvider);
  final realtime = ref.watch(realtimeServiceProvider);
  return LiveChatNotifier(repo, realtime, streamId);
});
