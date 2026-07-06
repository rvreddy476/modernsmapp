import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_realtime/realtime_event.dart';
import 'package:atpost_realtime/realtime_service.dart';
// AppUserRef + the shared user providers come through pulse_host's
// re-export of feature_contracts (alongside the Pulse chat contract).
import 'package:feature_pulse/host/pulse_host.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

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
