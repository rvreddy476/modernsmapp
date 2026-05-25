// Riverpod providers for the live-streaming-v2 surface. The legacy
// `live_repository.dart` providers stay in place for the v1 screens —
// these new providers serve the LiveKit-backed v2 screens
// (live_list_screen, live_viewer_screen, go_live_screen,
// live_broadcaster_screen) and use the v2 repo + models.

import 'package:atpost_app/data/models/live_stream_v2.dart';
import 'package:atpost_app/data/repositories/live_streams_repository.dart';
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
