import 'dart:async';

import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/services/realtime_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Holds real-time counter overrides for posts (likes, comments) and
/// user profiles (follower count). Widgets can watch these to get
/// instant updates without re-fetching from the API.

// ---------- Post counter overrides ----------

/// Map of postId -> latest like count received via WebSocket.
class PostCountersNotifier extends StateNotifier<Map<String, PostCounters>> {
  final RealtimeService _realtime;
  StreamSubscription? _sub;

  PostCountersNotifier(this._realtime) : super({}) {
    _sub = _realtime.events.listen(_onEvent);
  }

  void _onEvent(RealtimeEvent event) {
    if (event is PostLikedEvent) {
      final postId = event.postId;
      if (postId.isEmpty) return;
      final current = state[postId] ?? const PostCounters();
      final newLikes = event.likeCount ?? (current.likeCount ?? 0) + 1;
      state = {
        ...state,
        postId: current.copyWith(likeCount: newLikes),
      };
    } else if (event is PostCommentedEvent) {
      final postId = event.postId;
      if (postId.isEmpty) return;
      final current = state[postId] ?? const PostCounters();
      final newComments = event.commentCount ?? (current.commentCount ?? 0) + 1;
      state = {
        ...state,
        postId: current.copyWith(commentCount: newComments),
      };
    } else if (event is PostInteractionEvent) {
      final postId = event.postId;
      if (postId.isEmpty) return;
      final current = state[postId] ?? const PostCounters();
      state = {
        ...state,
        postId: current.copyWith(
          likeCount: event.likes ?? current.likeCount,
          commentCount: event.comments ?? current.commentCount,
        ),
      };
    }
  }

  @override
  void dispose() {
    _sub?.cancel();
    super.dispose();
  }
}

class PostCounters {
  final int? likeCount;
  final int? commentCount;

  const PostCounters({this.likeCount, this.commentCount});

  PostCounters copyWith({int? likeCount, int? commentCount}) {
    return PostCounters(
      likeCount: likeCount ?? this.likeCount,
      commentCount: commentCount ?? this.commentCount,
    );
  }
}

final postCountersProvider =
    StateNotifierProvider<PostCountersNotifier, Map<String, PostCounters>>(
        (ref) {
  final realtime = ref.watch(realtimeServiceProvider);
  return PostCountersNotifier(realtime);
});

/// Convenience: watch the live like count for a specific post.
/// Returns null if no real-time update has been received yet.
final postLikeCountProvider =
    Provider.family<int?, String>((ref, postId) {
  final counters = ref.watch(postCountersProvider);
  return counters[postId]?.likeCount;
});

/// Convenience: watch the live comment count for a specific post.
final postCommentCountProvider =
    Provider.family<int?, String>((ref, postId) {
  final counters = ref.watch(postCountersProvider);
  return counters[postId]?.commentCount;
});

// ---------- Follower counter overrides ----------

/// Tracks real-time follower count changes pushed via user.followed events.
class FollowerCounterNotifier extends StateNotifier<Map<String, int>> {
  final RealtimeService _realtime;
  StreamSubscription? _sub;

  FollowerCounterNotifier(this._realtime) : super({}) {
    _sub = _realtime.events.listen(_onEvent);
  }

  void _onEvent(RealtimeEvent event) {
    if (event is UserFollowedEvent) {
      final userId = event.followedId;
      if (userId.isEmpty) return;

      if (event.followerCount != null) {
        // Server sent the absolute count — use it directly.
        state = {...state, userId: event.followerCount!};
      } else {
        // No absolute count; increment what we have.
        final current = state[userId] ?? 0;
        state = {...state, userId: current + 1};
      }
    }
  }

  /// Seed the counter with the value fetched from the API so that
  /// subsequent increments are accurate.
  void seed(String userId, int count) {
    if (!state.containsKey(userId)) {
      state = {...state, userId: count};
    }
  }

  @override
  void dispose() {
    _sub?.cancel();
    super.dispose();
  }
}

final followerCounterProvider =
    StateNotifierProvider<FollowerCounterNotifier, Map<String, int>>((ref) {
  final realtime = ref.watch(realtimeServiceProvider);
  return FollowerCounterNotifier(realtime);
});

/// Convenience: watch the live follower count for a specific user.
/// Returns null if no real-time data is available.
final liveFollowerCountProvider =
    Provider.family<int?, String>((ref, userId) {
  final counters = ref.watch(followerCounterProvider);
  return counters[userId];
});
