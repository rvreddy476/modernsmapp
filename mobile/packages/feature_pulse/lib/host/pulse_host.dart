import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Pulse-specific host contract (live chat realtime). The shared user
/// projection ([AppUserRef]), current-user, and user-search providers
/// come from feature_contracts — re-exported here so Pulse screens keep a
/// single host import.
export 'package:feature_contracts/app_user.dart';

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

/// Live chat messages for Pulse conversations. Defaults to silence so
/// the chat screen still works on REST polling alone.
final pulseChatEventsProvider =
    Provider<Stream<PulseChatWireMessage>>((_) => const Stream.empty());
