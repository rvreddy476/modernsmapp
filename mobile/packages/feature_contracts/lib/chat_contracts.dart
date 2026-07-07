import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Start (or fetch) a 1:1 direct conversation with [userId] and return its
/// conversation id to navigate to (`/chat/:id`), or null on failure.
/// Backed by the app's chat repository so social/profile surfaces get a
/// "Message" button without depending on the chat stack. Default: null.
typedef AppStartConversation = Future<String?> Function(String userId);
final appStartConversationProvider =
    Provider<AppStartConversation>((_) => (_) async => null);

/// Per-friend unread message counts, keyed by the *other* user's id, for
/// the badge on the Friends list. Derived from the app's live conversation
/// list so it updates in real time. Default: empty.
final appConversationUnreadProvider =
    Provider<Map<String, int>>((_) => const {});
