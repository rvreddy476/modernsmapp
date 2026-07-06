import 'package:flutter_riverpod/flutter_riverpod.dart';

/// What Pulse needs from its host app — and nothing more. The host
/// (atpost_app) overrides the three providers below at its root
/// ProviderScope; the feature never imports host models, the host user
/// session, or the host realtime transport directly.

/// Minimal projection of the host's signed-in user.
class PulseHostUser {
  const PulseHostUser({
    required this.id,
    required this.displayName,
    this.username,
    this.avatarUrl,
    this.city,
  });

  final String id;
  final String displayName;
  final String? username;
  final String? avatarUrl;
  final String? city;

  bool get hasAvatar => (avatarUrl ?? '').isNotEmpty;
}

/// A chat message pushed over the host's realtime channel, already
/// filtered to Pulse conversations and flattened to the fields the chat
/// screen consumes.
class PulseChatWireMessage {
  const PulseChatWireMessage({
    required this.conversationId,
    required this.messageId,
    required this.senderId,
    required this.messageType,
    required this.text,
    required this.createdAt,
    this.mediaId,
    this.payload = const {},
  });

  final String conversationId;
  final String messageId;
  final String senderId;
  final String messageType;
  final String text;
  final DateTime createdAt;
  final String? mediaId;
  final Map<String, dynamic> payload;
}

/// The host's signed-in user, or null when signed out / not provided.
/// Host override maps its own user model into [PulseHostUser].
final pulseHostUserProvider = FutureProvider<PulseHostUser?>((_) async => null);

/// Live chat messages for Pulse conversations. Defaults to silence so
/// the chat screen still works on REST polling alone.
final pulseChatEventsProvider =
    Provider<Stream<PulseChatWireMessage>>((_) => const Stream.empty());

/// Host user search (vouch picker). Defaults to no results.
typedef PulseUserSearch = Future<List<PulseHostUser>> Function(String query);
final pulseUserSearchProvider =
    Provider<PulseUserSearch>((_) => (_) async => const []);
