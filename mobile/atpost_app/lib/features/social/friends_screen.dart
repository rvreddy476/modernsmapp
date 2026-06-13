import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:atpost_app/features/social/widgets/add_friends_sheet.dart';
import 'package:atpost_app/features/social/widgets/friend_requests_sheet.dart';
import 'package:atpost_app/features/social/widgets/friends_common.dart';
import 'package:atpost_app/features/social/widgets/trusted_circle_sheet.dart';
import 'package:atpost_app/providers/chat_provider.dart';
import 'package:atpost_app/providers/social_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// SURFACE 1 — Friends Home.
///
/// A calm, single-column home: a bold title with an "{N} in orbit ·
/// {M} pending" subtitle, a "Pulse · Live now" strip of friend activity,
/// the Trusted Circle and new-requests entry cards, and the full friends
/// list. The three deeper surfaces (Trusted Circle, Add friends, Requests)
/// open as bottom sheets from here.
///
/// The data layer is unchanged — every section reads the existing
/// social providers; only the layout is new.
class FriendsScreen extends ConsumerStatefulWidget {
  const FriendsScreen({super.key});

  @override
  ConsumerState<FriendsScreen> createState() => _FriendsScreenState();
}

enum _SortMode { recent, nameAsc, online }

class _FriendsScreenState extends ConsumerState<FriendsScreen> {
  final TextEditingController _searchController = TextEditingController();
  final FocusNode _searchFocus = FocusNode();
  bool _searching = false;
  _SortMode _sort = _SortMode.recent;

  @override
  void dispose() {
    _searchController.dispose();
    _searchFocus.dispose();
    super.dispose();
  }

  void _snack(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(message),
        backgroundColor: AppColors.bgSecondary,
        behavior: SnackBarBehavior.floating,
        duration: const Duration(seconds: 2),
      ),
    );
  }

  Future<void> _openConversation(String userId) async {
    final messenger = ScaffoldMessenger.of(context);
    try {
      final conversation = await ref
          .read(chatRepositoryProvider)
          .createDirectConversation(userId);
      if (!mounted) return;
      context.push('/chat/${conversation.id}');
    } catch (_) {
      messenger.showSnackBar(
        const SnackBar(content: Text("Couldn't open the conversation")),
      );
    }
  }

  void _toggleSearch() {
    setState(() => _searching = !_searching);
    if (_searching) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        _searchFocus.requestFocus();
      });
    } else {
      _searchController.clear();
    }
  }

  void _focusSearch() {
    setState(() => _searching = true);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _searchFocus.requestFocus();
    });
  }

  void _openSort() {
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (sheetCtx) {
        Widget option(_SortMode mode, IconData icon, String label) {
          final active = _sort == mode;
          return ListTile(
            leading: Icon(icon,
                color: active
                    ? AppColors.postbookPrimary
                    : AppColors.textSecondary),
            title: Text(
              label,
              style: TextStyle(
                color: active
                    ? AppColors.postbookPrimary
                    : AppColors.textPrimary,
                fontWeight: active ? FontWeight.w700 : FontWeight.w500,
              ),
            ),
            trailing: active
                ? const Icon(Icons.check_rounded,
                    color: AppColors.postbookPrimary, size: 18)
                : null,
            onTap: () {
              setState(() => _sort = mode);
              Navigator.of(sheetCtx).pop();
            },
          );
        }

        return SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Center(child: SheetGrabber()),
              const Padding(
                padding: EdgeInsets.fromLTRB(18, 6, 18, 4),
                child: Align(
                  alignment: Alignment.centerLeft,
                  child: EyebrowLabel('SORT FRIENDS BY'),
                ),
              ),
              option(_SortMode.recent, Icons.schedule_rounded,
                  'Recently added'),
              option(_SortMode.nameAsc, Icons.sort_by_alpha_rounded,
                  'Name (A–Z)'),
              option(_SortMode.online, Icons.bolt_rounded, 'Online first'),
              const SizedBox(height: 8),
            ],
          ),
        );
      },
    );
  }

  List<User> _applySearchAndSort(List<User> friends, Map<String, bool> presence) {
    final q = _searchController.text.trim().toLowerCase();
    var list = q.isEmpty
        ? List<User>.from(friends)
        : friends
            .where((u) =>
                u.displayName.toLowerCase().contains(q) ||
                u.username.toLowerCase().contains(q))
            .toList();
    switch (_sort) {
      case _SortMode.recent:
        break; // provider order is the backend's "recently added" order.
      case _SortMode.nameAsc:
        list.sort((a, b) => a.displayName
            .toLowerCase()
            .compareTo(b.displayName.toLowerCase()));
      case _SortMode.online:
        list.sort((a, b) {
          final ao = presence[a.id] == true ? 0 : 1;
          final bo = presence[b.id] == true ? 0 : 1;
          return ao.compareTo(bo);
        });
    }
    return list;
  }

  @override
  Widget build(BuildContext context) {
    final friendsAsync = ref.watch(friendsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: friendsAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(
              color: AppColors.postbookPrimary,
            ),
          ),
          error: (_, _) => _errorState(),
          data: (friends) => RefreshIndicator(
            color: AppColors.postbookPrimary,
            backgroundColor: AppColors.bgSecondary,
            onRefresh: () async {
              ref.invalidate(friendsProvider);
              ref.invalidate(friendRequestsProvider);
              ref.invalidate(friendSuggestionsProvider);
              ref.invalidate(closeFriendsProvider);
              ref.invalidate(friendsPresenceProvider);
            },
            child: _content(friends),
          ),
        ),
      ),
    );
  }

  Widget _content(List<User> friends) {
    final allRequests =
        ref.watch(friendRequestsProvider).valueOrNull ?? const [];
    final pending =
        allRequests.where((r) => r.direction == 'received').toList();
    final presence =
        ref.watch(friendsPresenceProvider).valueOrNull ?? const {};
    final closeFriends =
        ref.watch(closeFriendsProvider).valueOrNull ?? const <User>[];
    // Live per-friend unread message counts (same source as the Messages
    // screen — see conversationUnreadByUserProvider).
    final unread = ref.watch(conversationUnreadByUserProvider);

    final listed = _applySearchAndSort(friends, presence);

    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.only(bottom: 110),
      children: [
        _header(friends.length, pending.length),
        if (_searching) _searchBar(),
        if (friends.isNotEmpty) ...[
          const SizedBox(height: 6),
          _pulseSection(friends, presence),
        ],
        const SizedBox(height: 14),
        _trustedCircleCard(friends, closeFriends.length),
        if (pending.isNotEmpty) ...[
          const SizedBox(height: 10),
          _newRequestsCard(pending),
        ],
        const SizedBox(height: 8),
        _allFriendsHeader(friends.length),
        if (listed.isEmpty)
          _emptyFriends(
            friends.isEmpty
                ? 'No friends yet — tap the add icon to grow your orbit.'
                : 'No friends match your search.',
          )
        else
          ...listed.map(
            (u) => _FriendRow(
              user: u,
              online: presence[u.id] == true,
              unread: unread[u.id] ?? 0,
              onTap: () => context.push('/profile/${u.id}'),
              onMessage: () => _openConversation(u.id),
            ),
          ),
      ],
    );
  }

  // ---------------- Header ----------------

  Widget _header(int friendCount, int pendingCount) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 12, 14, 6),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text(
                  'Friends',
                  style: TextStyle(
                    color: AppColors.textPrimary,
                    fontSize: 30,
                    fontWeight: FontWeight.w800,
                    letterSpacing: -0.8,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  '$friendCount in orbit · $pendingCount pending',
                  style: const TextStyle(
                    color: AppColors.textTertiary,
                    fontSize: 12.5,
                    fontWeight: FontWeight.w400,
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(width: 10),
          CircleIconButton(
            icon: _searching ? Icons.close_rounded : Icons.search_rounded,
            onTap: _toggleSearch,
          ),
          const SizedBox(width: 8),
          CircleIconButton(
            icon: Icons.grid_view_rounded,
            onTap: () => showAddFriendsSheet(
              context,
              contactsOnAtpost: 0,
              onSearchUsername: _focusSearch,
            ),
          ),
        ],
      ),
    );
  }

  Widget _searchBar() {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 10, 18, 0),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            const Icon(Icons.search,
                size: 18, color: AppColors.textTertiary),
            const SizedBox(width: 10),
            Expanded(
              child: TextField(
                controller: _searchController,
                focusNode: _searchFocus,
                onChanged: (_) => setState(() {}),
                style: const TextStyle(
                  color: AppColors.textPrimary,
                  fontSize: 14,
                ),
                decoration: const InputDecoration(
                  isCollapsed: true,
                  contentPadding: EdgeInsets.symmetric(vertical: 13),
                  border: InputBorder.none,
                  hintText: 'Search your friends…',
                  hintStyle: TextStyle(
                    color: AppColors.textTertiary,
                    fontSize: 14,
                  ),
                ),
              ),
            ),
            if (_searchController.text.isNotEmpty)
              GestureDetector(
                onTap: () {
                  _searchController.clear();
                  setState(() {});
                },
                child: const Icon(Icons.close_rounded,
                    size: 16, color: AppColors.textTertiary),
              ),
          ],
        ),
      ),
    );
  }

  // ---------------- Pulse · Live now ----------------

  Widget _pulseSection(List<User> friends, Map<String, bool> presence) {
    // No backend feed of friend live-activity exists — populate the strip
    // from real friends as placeholders, cycling the three tag types.
    final picks = friends.take(3).toList();
    if (picks.isEmpty) return const SizedBox.shrink();

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Padding(
          padding: EdgeInsets.fromLTRB(18, 10, 18, 8),
          child: EyebrowLabel('PULSE · LIVE NOW'),
        ),
        SizedBox(
          height: 132,
          child: ListView.separated(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(horizontal: 18),
            itemCount: picks.length,
            separatorBuilder: (_, _) => const SizedBox(width: 10),
            itemBuilder: (context, i) {
              return _PulseCard(
                user: picks[i],
                kind: _PulseKind.values[i % _PulseKind.values.length],
                onTap: () => context.push('/profile/${picks[i].id}'),
              );
            },
          ),
        ),
      ],
    );
  }

  // ---------------- Trusted Circle entry card ----------------

  Widget _trustedCircleCard(List<User> friends, int closeCount) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 18),
      child: GestureDetector(
        onTap: () {
          if (friends.isEmpty) {
            _snack('Add friends first, then build your Trusted Circle.');
            return;
          }
          showTrustedCircleSheet(context, friends: friends);
        },
        child: Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(18),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            children: [
              Container(
                width: 44,
                height: 44,
                decoration: BoxDecoration(
                  color: AppColors.posttubePrimary.withValues(alpha: 0.14),
                  borderRadius: BorderRadius.circular(13),
                ),
                alignment: Alignment.center,
                child: const Icon(Icons.verified_user_rounded,
                    size: 22, color: AppColors.posttubePrimary),
              ),
              const SizedBox(width: 13),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Text(
                      'Trusted Circle',
                      style: TextStyle(
                        color: AppColors.textPrimary,
                        fontSize: 14.5,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    const SizedBox(height: 2),
                    Text(
                      closeCount == 0
                          ? 'Pick the people who see your inner posts'
                          : '$closeCount ${closeCount == 1 ? 'person sees' : 'people see'} your inner posts',
                      style: const TextStyle(
                        color: AppColors.textTertiary,
                        fontSize: 11.5,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 6),
              const Icon(Icons.chevron_right_rounded,
                  color: AppColors.textMuted),
            ],
          ),
        ),
      ),
    );
  }

  // ---------------- New requests entry card ----------------

  Widget _newRequestsCard(List<FriendRequest> pending) {
    final names = pending
        .take(3)
        .map((r) => r.senderName.trim().split(' ').first)
        .where((n) => n.isNotEmpty)
        .toList();
    final m = pending.length;
    final sub = names.isEmpty
        ? 'Tap to review'
        : m > names.length
            ? '${names.join(', ')} and ${m - names.length} more'
            : names.join(', ');

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 18),
      child: GestureDetector(
        onTap: () => showFriendRequestsSheet(context),
        child: Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(18),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            children: [
              SizedBox(
                width: 44,
                height: 44,
                child: _AvatarStack(requests: pending.take(3).toList()),
              ),
              const SizedBox(width: 13),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      '$m new ${m == 1 ? 'request' : 'requests'}',
                      style: const TextStyle(
                        color: AppColors.textPrimary,
                        fontSize: 14.5,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    const SizedBox(height: 2),
                    Text(
                      sub,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: AppColors.textTertiary,
                        fontSize: 11.5,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 6),
              const Icon(Icons.chevron_right_rounded,
                  color: AppColors.textMuted),
            ],
          ),
        ),
      ),
    );
  }

  // ---------------- All friends ----------------

  Widget _allFriendsHeader(int count) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 14, 18, 6),
      child: Row(
        children: [
          Expanded(child: EyebrowLabel('ALL FRIENDS · $count')),
          GestureDetector(
            onTap: _openSort,
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: const [
                Icon(Icons.swap_vert_rounded,
                    size: 15, color: AppColors.postbookPrimary),
                SizedBox(width: 3),
                Text(
                  'Sort',
                  style: TextStyle(
                    color: AppColors.postbookPrimary,
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _emptyFriends(String message) {
    return Container(
      margin: const EdgeInsets.fromLTRB(18, 4, 18, 12),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          const Icon(Icons.person_off_outlined,
              color: AppColors.textMuted),
          const SizedBox(width: 12),
          Expanded(
            child: Text(
              message,
              style: const TextStyle(
                color: AppColors.textTertiary,
                fontSize: 13,
                height: 1.4,
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _errorState() {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.people_alt_outlined,
              color: AppColors.textMuted, size: 38),
          const SizedBox(height: 12),
          const Text(
            'Could not load your friends.',
            style: TextStyle(color: AppColors.textSecondary),
          ),
          const SizedBox(height: 8),
          TextButton(
            onPressed: () => ref.invalidate(friendsProvider),
            child: const Text('Retry'),
          ),
        ],
      ),
    );
  }
}

// ============================================================
// Widgets
// ============================================================

/// The three Pulse tag types, cycled across the placeholder cards.
enum _PulseKind { live, audio, reel }

class _PulseCard extends StatelessWidget {
  const _PulseCard({
    required this.user,
    required this.kind,
    required this.onTap,
  });

  final User user;
  final _PulseKind kind;
  final VoidCallback onTap;

  ({String tag, Color color, String status}) get _meta => switch (kind) {
        _PulseKind.live => (
            tag: 'LIVE',
            color: AppColors.liveRed,
            status: 'going live now',
          ),
        _PulseKind.audio => (
            tag: 'AUDIO',
            color: AppColors.posttubePrimary,
            status: 'in audio room',
          ),
        _PulseKind.reel => (
            tag: 'REEL',
            color: AppColors.accentPurple,
            status: 'posted a flick',
          ),
      };

  @override
  Widget build(BuildContext context) {
    final m = _meta;
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: 142,
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(16),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Container(
                  width: 6,
                  height: 6,
                  decoration: BoxDecoration(
                    color: m.color,
                    shape: BoxShape.circle,
                  ),
                ),
                const SizedBox(width: 5),
                Text(
                  m.tag,
                  style: TextStyle(
                    color: m.color,
                    fontSize: 9,
                    fontWeight: FontWeight.w800,
                    letterSpacing: 0.6,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 10),
            FriendAvatar(
              name: user.displayName,
              url: user.hasAvatar ? user.avatarUrl : null,
              size: 40,
            ),
            const SizedBox(height: 8),
            Text(
              user.displayName,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                color: AppColors.textPrimary,
                fontSize: 12.5,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 1),
            Text(
              m.status,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                color: AppColors.textTertiary,
                fontSize: 10.5,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// Three small overlapping avatars for the new-requests entry card.
class _AvatarStack extends StatelessWidget {
  const _AvatarStack({required this.requests});

  final List<FriendRequest> requests;

  @override
  Widget build(BuildContext context) {
    return Stack(
      clipBehavior: Clip.none,
      children: [
        for (var i = 0; i < requests.length; i++)
          Positioned(
            left: i * 13.0,
            top: 6,
            child: Container(
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                border:
                    Border.all(color: AppColors.bgSecondary, width: 2),
              ),
              child: FriendAvatar(
                name: requests[i].senderName,
                size: 30,
              ),
            ),
          ),
      ],
    );
  }
}

/// A row in the "All friends" list.
class _FriendRow extends StatelessWidget {
  const _FriendRow({
    required this.user,
    required this.online,
    required this.unread,
    required this.onTap,
    required this.onMessage,
  });

  final User user;
  final bool online;
  final int unread;
  final VoidCallback onTap;
  final VoidCallback onMessage;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 8),
          child: Row(
            children: [
              Stack(
                children: [
                  FriendAvatar(
                    name: user.displayName,
                    url: user.hasAvatar ? user.avatarUrl : null,
                    size: 44,
                  ),
                  if (online)
                    Positioned(
                      right: 0,
                      bottom: 0,
                      child: Container(
                        width: 12,
                        height: 12,
                        decoration: BoxDecoration(
                          color: AppColors.onlineGreen,
                          shape: BoxShape.circle,
                          border: Border.all(
                            color: AppColors.bgPrimary,
                            width: 2.2,
                          ),
                        ),
                      ),
                    ),
                ],
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Flexible(
                          child: Text(
                            user.displayName,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: const TextStyle(
                              color: AppColors.textPrimary,
                              fontSize: 14,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                        ),
                        if (user.isVerified) ...[
                          const SizedBox(width: 4),
                          const Icon(
                            Icons.verified,
                            size: 14,
                            color: AppColors.postbookPrimary,
                          ),
                        ],
                      ],
                    ),
                    const SizedBox(height: 1),
                    Text(
                      online
                          ? 'Active now · @${user.username}'
                          : '@${user.username}',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: online
                            ? AppColors.onlineGreen
                            : AppColors.textTertiary,
                        fontSize: 11,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 8),
              GestureDetector(
                onTap: onMessage,
                child: Stack(
                  clipBehavior: Clip.none,
                  children: [
                    Container(
                      width: 34,
                      height: 34,
                      decoration: BoxDecoration(
                        color: AppColors.bgCard,
                        shape: BoxShape.circle,
                        border: Border.all(color: AppColors.borderSubtle),
                      ),
                      alignment: Alignment.center,
                      child: const Icon(
                        Icons.chat_bubble_outline_rounded,
                        size: 16,
                        color: AppColors.textSecondary,
                      ),
                    ),
                    if (unread > 0)
                      Positioned(
                        right: -4,
                        top: -4,
                        child: Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 5,
                            vertical: 2,
                          ),
                          constraints: const BoxConstraints(minWidth: 18),
                          decoration: BoxDecoration(
                            color: AppColors.postbookPrimary,
                            borderRadius: BorderRadius.circular(9),
                            border: Border.all(
                              color: AppColors.bgPrimary,
                              width: 2,
                            ),
                          ),
                          alignment: Alignment.center,
                          child: Text(
                            unread > 99 ? '99+' : '$unread',
                            style: const TextStyle(
                              color: Colors.white,
                              fontSize: 10,
                              fontWeight: FontWeight.w700,
                              height: 1.1,
                            ),
                          ),
                        ),
                      ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
