import 'package:atpost_realtime/realtime_event.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_realtime/realtime_service.dart';
import 'package:feature_pulse/host/pulse_host.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// App-side implementations of feature_pulse's host contracts. This is
/// the ONLY place the app's user/session/realtime types meet the Pulse
/// feature — keep it that way so the package stays independently
/// buildable and testable.
List<Override> pulseHostBindings() => [
  // Signed-in user projection (deck avatar, onboarding seed name, city gate).
  pulseHostUserProvider.overrideWith((ref) async {
    final user = await ref.watch(currentUserProvider.future);
    return PulseHostUser(
      id: user.id,
      displayName: user.displayName,
      username: user.username,
      avatarUrl: user.hasAvatar ? user.avatarUrl : null,
      city: user.location,
    );
  }),

  // Live chat messages: filter the shared realtime channel down to the
  // chat wire type and flatten it into the Pulse-owned shape.
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

  // Vouch-picker user search.
  pulseUserSearchProvider.overrideWith((ref) {
    return (String query) async {
      final result =
          await ref.read(userRepositoryProvider).searchUsers(query, limit: 10);
      return result.users
          .map((u) => PulseHostUser(
                id: u.id,
                displayName: u.displayName,
                username: u.username,
                avatarUrl: u.hasAvatar ? u.avatarUrl : null,
                city: u.location,
              ))
          .toList();
    };
  }),
];
