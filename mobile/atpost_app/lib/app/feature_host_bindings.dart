import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/features/hashtag_feed/state/hashtag_feed_notifier.dart';
import 'package:atpost_app/features/monetization/widgets/paywall_preview.dart';
import 'package:atpost_app/features/shell/shell_providers.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_realtime/realtime_event.dart';
import 'package:atpost_realtime/realtime_service.dart';
import 'package:feature_contracts/feature_contracts.dart';
// AppUserRef + the shared user providers come through pulse_host's
// re-export of feature_contracts (alongside the Pulse chat contract).
import 'package:feature_pulse/host/pulse_host.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Relationship actions on another user, backed by the app's
/// UserRepository. Bound to [appUserActionsProvider] so the social UI
/// (post header follow toggle, "don't recommend this channel") never
/// imports the repository directly.
class _AppUserActions implements AppUserActions {
  _AppUserActions(this._repo);
  final UserRepository _repo;
  @override
  Future<void> follow(String userId) => _repo.followUser(userId);
  @override
  Future<void> unfollow(String userId) => _repo.unfollowUser(userId);
  @override
  Future<void> mute(String userId) => _repo.muteUser(userId);
}

/// App-side implementations of the cross-feature host contracts. This is
/// the ONE place the app's user/session/realtime types meet the feature
/// packages — everything below maps an app type onto a feature-facing
/// contract so the packages stay decoupled from the app and each other.

AppUserRef _toRef(User u) => AppUserRef(
  id: u.id,
  displayName: u.displayName,
  username: u.username,
  avatarUrl: u.hasAvatar ? u.avatarUrl : null,
  city: u.location,
);

List<Override> featureHostBindings() => [
  // Shared: the signed-in user (Pulse city gate + deck avatar, Mopedu
  // city gate + onboarding prefill, Wallet, etc.).
  currentAppUserProvider.overrideWith((ref) async {
    final user = await ref.watch(currentUserProvider.future);
    return _toRef(user);
  }),

  // Shared: AtPost user search (Pulse vouch, Wallet send, slambook invite).
  appUserSearchProvider.overrideWith((ref) {
    return (String query) async {
      final result =
          await ref.read(userRepositoryProvider).searchUsers(query, limit: 20);
      return result.users.map(_toRef).toList();
    };
  }),

  // Social: batch author hydration + the viewer's following set.
  appUserBatchProvider.overrideWith((ref) {
    return (List<String> ids) async {
      final users = await ref.read(userRepositoryProvider).getUsersBatch(ids);
      return users.map(_toRef).toList();
    };
  }),
  appFollowingIdsProvider.overrideWith((ref) {
    return (String userId) =>
        ref.read(userRepositoryProvider).getFollowingIds(userId);
  }),

  // Social UI: follow / unfollow / mute another user.
  appUserActionsProvider
      .overrideWith((ref) => _AppUserActions(ref.read(userRepositoryProvider))),

  // Social UI: a hashtag tapped in a post body. On the home shell, switch
  // to the #Hashtag tab and select it; otherwise push /hashtag/:tag.
  appHashtagTapProvider.overrideWith((ref) {
    return (BuildContext context, String tag) {
      try {
        ref.read(homeFeedTabProvider.notifier).state = 2;
        ref.read(hashtagFeedProvider.notifier).selectHashtagByName(tag);
      } catch (_) {
        context.push('/hashtag/${Uri.encodeComponent(tag)}');
      }
    };
  }),

  // Social UI: the monetization paywall preview for a redacted post body.
  appPaywallBuilderProvider.overrideWith((ref) {
    return (BuildContext context,
            {required String creatorId, String? creatorName}) =>
        PaywallPreview(creatorId: creatorId, creatorName: creatorName);
  }),

  // Pulse-specific: live chat messages off the shared realtime channel,
  // filtered to the chat wire type and flattened to the Pulse shape.
  pulseChatEventsProvider.overrideWith((ref) {
    final realtime = ref.watch(realtimeServiceProvider);
    return realtime.events.where((e) => e is ChatMessageEvent).map((e) {
      final msg = e as ChatMessageEvent;
      return PulseChatWireMessage(
        conversationId: msg.conversationId,
        messageId: msg.messageId,
        senderId: msg.senderId,
        messageType: msg.messageType,
        text: msg.text,
        createdAt: msg.createdAt,
        mediaId: msg.mediaId,
        payload: msg.payload,
      );
    });
  }),
];
