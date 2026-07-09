/// Per-conversation presence rollup returned by
/// `GET /v1/conversations/:id/presence` (M1 backend).
///
/// `activeUsers` and `typingUsers` are populated for direct chats and for
/// groups with ≤100 members. For larger groups the server omits them and
/// sets [isBigGroup] = true, in which case the UI should fall back to
/// rendering just the count.
class ConversationPresence {
  final int activeCount;
  final List<String> activeUsers;
  final List<String> typingUsers;
  final bool isBigGroup;

  const ConversationPresence({
    required this.activeCount,
    required this.activeUsers,
    required this.typingUsers,
    required this.isBigGroup,
  });

  factory ConversationPresence.fromJson(Map<String, dynamic> json) {
    final activeRaw = json['active_users'];
    final typingRaw = json['typing_users'];
    return ConversationPresence(
      activeCount: (json['active_count'] as num?)?.toInt() ?? 0,
      activeUsers: activeRaw is List
          ? activeRaw.whereType<String>().toList(growable: false)
          : const <String>[],
      typingUsers: typingRaw is List
          ? typingRaw.whereType<String>().toList(growable: false)
          : const <String>[],
      isBigGroup: json['is_big_group'] == true,
    );
  }

  static const empty = ConversationPresence(
    activeCount: 0,
    activeUsers: <String>[],
    typingUsers: <String>[],
    isBigGroup: false,
  );
}
