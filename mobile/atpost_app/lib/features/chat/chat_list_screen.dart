import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:atpost_app/providers/chat_provider.dart';
import 'package:atpost_app/providers/social_provider.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/realtime_service.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Optimized Chat Messenger System.
/// Features: Resilient friend list loading, Direct Chat creation, and performance optimizations.
class ChatListScreen extends ConsumerStatefulWidget {
  const ChatListScreen({super.key});

  @override
  ConsumerState<ChatListScreen> createState() => _ChatListScreenState();
}

class _ChatListScreenState extends ConsumerState<ChatListScreen> {
  final TextEditingController _searchController = TextEditingController();
  bool _isSelectingFriend = false;

  @override
  void initState() {
    super.initState();
    _searchController.addListener(() {
      ref.read(chatSearchQueryProvider.notifier).state = _searchController.text;
    });
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      body: SafeArea(
        child: Column(
          children: [
            _buildAppBar(),
            Expanded(
              child: _isSelectingFriend
                  ? _buildFriendSelector()
                  : _buildConversationList(),
            ),
          ],
        ),
      ),
      floatingActionButton: FloatingActionButton(
        onPressed: () =>
            setState(() => _isSelectingFriend = !_isSelectingFriend),
        backgroundColor: AppColors.postbookPrimary,
        child: Icon(
          _isSelectingFriend ? Icons.close : Icons.chat_bubble_outline,
          color: Colors.white,
        ),
      ),
    );
  }

  Widget _buildAppBar() {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Row(
        children: [
          GlassIconButton(
            icon: Icons.arrow_back,
            tooltip: 'Back',
            onPressed: () => context.pop(),
          ),
          const SizedBox(width: 12),
          Text(
            _isSelectingFriend ? 'New Chat' : 'Messages',
            style: AppTextStyles.h1,
          ),
          const Spacer(),
          if (!_isSelectingFriend)
            GlassIconButton(
              icon: Icons.search,
              tooltip: 'Search',
              onPressed: () {},
            ),
        ],
      ),
    );
  }

  Widget _buildConversationList() {
    final conversationsAsync = ref.watch(filteredConversationsProvider);
    final activeFilter = ref.watch(chatActiveFilterProvider);
    final currentUserId = ref.watch(authServiceProvider).userId;

    return Column(
      children: [
        _buildRequestsEntry(),
        _buildFriendsStrip(),
        _buildFilters(activeFilter),
        Expanded(
          child: conversationsAsync.when(
            loading: () => const Center(child: CircularProgressIndicator()),
            error: (err, stack) => Center(
              child: Column(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  const Icon(
                    Icons.error_outline,
                    size: 48,
                    color: Colors.white24,
                  ),
                  const SizedBox(height: 16),
                  Text('Could not load messages', style: AppTextStyles.h3),
                  const SizedBox(height: 8),
                  TextButton(
                    onPressed: () {
                      ref.invalidate(chatConversationsProvider);
                      ref.read(realtimeServiceProvider).reconnect();
                    },
                    child: const Text(
                      'Retry',
                      style: TextStyle(color: AppColors.postbookPrimary),
                    ),
                  ),
                ],
              ),
            ),
            data: (conversations) {
              if (conversations.isEmpty) {
                return Center(
                  child: Text(
                    'No messages yet',
                    style: AppTextStyles.bodySmall,
                  ),
                );
              }

              return ListView.builder(
                itemCount: conversations.length,
                padding: const EdgeInsets.symmetric(horizontal: 16),
                itemBuilder: (context, index) {
                  final convo = conversations[index];
                  final displayName = convo.displayNameFor(currentUserId);
                  return Padding(
                    padding: const EdgeInsets.only(bottom: 12),
                    child: _ConversationTile(
                      convo: convo,
                      displayName: displayName,
                      onTap: () => context.push('/chat/${convo.id}'),
                    ),
                  );
                },
              );
            },
          ),
        ),
      ],
    );
  }

  /// Entry point into the Message Requests folder (spec §3.3). Hidden when
  /// there are no pending requests so it does not clutter the inbox.
  Widget _buildRequestsEntry() {
    final requestsAsync = ref.watch(messageRequestsProvider);
    final count = requestsAsync.maybeWhen(
      data: (requests) => requests.length,
      orElse: () => 0,
    );
    if (count == 0) return const SizedBox.shrink();

    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
      child: GestureDetector(
        onTap: () => context.push('/chat/requests'),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(16),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            children: [
              const CircleAvatar(
                radius: 20,
                backgroundColor: Colors.white10,
                child: Icon(
                  Icons.mark_email_unread_outlined,
                  size: 20,
                  color: AppColors.postbookPrimary,
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('Message Requests', style: AppTextStyles.h3),
                    const SizedBox(height: 2),
                    Text(
                      count == 1
                          ? '1 pending request'
                          : '$count pending requests',
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ),
              Container(
                padding: const EdgeInsets.symmetric(
                  horizontal: 8,
                  vertical: 4,
                ),
                decoration: BoxDecoration(
                  color: AppColors.postbookPrimary,
                  borderRadius: BorderRadius.circular(12),
                ),
                child: Text(
                  '$count',
                  style: const TextStyle(
                    color: Colors.white,
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              const SizedBox(width: 4),
              const Icon(
                Icons.chevron_right,
                color: Colors.white24,
              ),
            ],
          ),
        ),
      ),
    );
  }

  /// A horizontal strip of every accepted friend, so the user can start a
  /// DM with anyone — not just people they already have a conversation
  /// with. Online friends carry a green dot driven by [friendsPresenceProvider]
  /// and are sorted to the front. Hidden while selecting a friend.
  Widget _buildFriendsStrip() {
    final friendsAsync = ref.watch(friendsProvider);
    final presence =
        ref.watch(friendsPresenceProvider).valueOrNull ?? const <String, bool>{};

    return friendsAsync.maybeWhen(
      data: (friends) {
        if (friends.isEmpty) return const SizedBox.shrink();

        // Online friends first, otherwise keep the backend order.
        final sorted = List<User>.from(friends)
          ..sort((a, b) {
            final ao = presence[a.id] == true ? 0 : 1;
            final bo = presence[b.id] == true ? 0 : 1;
            return ao.compareTo(bo);
          });
        final onlineCount =
            friends.where((u) => presence[u.id] == true).length;

        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 16, 6),
              child: Row(
                children: [
                  Text('Friends', style: AppTextStyles.h3),
                  const SizedBox(width: 8),
                  if (onlineCount > 0)
                    Text(
                      '$onlineCount online',
                      style: const TextStyle(
                        color: AppColors.onlineGreen,
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                ],
              ),
            ),
            SizedBox(
              height: 86,
              child: ListView.builder(
                scrollDirection: Axis.horizontal,
                padding: const EdgeInsets.symmetric(horizontal: 12),
                itemCount: sorted.length,
                itemBuilder: (context, index) {
                  final friend = sorted[index];
                  return _FriendStripItem(
                    friend: friend,
                    online: presence[friend.id] == true,
                    onTap: () => _startDirectChat(friend),
                  );
                },
              ),
            ),
          ],
        );
      },
      orElse: () => const SizedBox.shrink(),
    );
  }

  Widget _buildFilters(int activeFilter) {
    final filters = ['All', 'Unread', 'Groups', 'Archived'];
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Row(
        children: List.generate(filters.length, (index) {
          final isSelected = activeFilter == index;
          return Padding(
            padding: const EdgeInsets.only(right: 8),
            child: ChoiceChip(
              label: Text(filters[index]),
              selected: isSelected,
              onSelected: (_) =>
                  ref.read(chatActiveFilterProvider.notifier).state = index,
              backgroundColor: Colors.transparent,
              selectedColor: AppColors.postbookPrimary,
              labelStyle: TextStyle(
                color: isSelected ? Colors.white : Colors.white60,
                fontSize: 13,
              ),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(20),
                side: BorderSide(
                  color: isSelected
                      ? AppColors.postbookPrimary
                      : Colors.white10,
                ),
              ),
            ),
          );
        }),
      ),
    );
  }

  Widget _buildFriendSelector() {
    final friendsAsync = ref.watch(friendsProvider);

    return friendsAsync.when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, _) => Center(
        child: Text('Error loading friends', style: AppTextStyles.bodySmall),
      ),
      data: (friends) {
        if (friends.isEmpty) {
          return Center(
            child: Text(
              'No friends found to start a chat',
              style: AppTextStyles.bodySmall,
            ),
          );
        }
        return ListView.builder(
          itemCount: friends.length,
          padding: const EdgeInsets.symmetric(horizontal: 16),
          itemBuilder: (context, index) {
            final friend = friends[index];
            return ListTile(
              onTap: () => _startDirectChat(friend),
              leading: CircleAvatar(
                backgroundImage: NetworkImage(friend.avatarUrl),
              ),
              title: Text(friend.displayName, style: AppTextStyles.h3),
              subtitle: Text(
                '@${friend.username}',
                style: AppTextStyles.bodySmall,
              ),
              trailing: const Icon(
                Icons.arrow_forward_ios,
                size: 14,
                color: Colors.white24,
              ),
            );
          },
        );
      },
    );
  }

  Future<void> _startDirectChat(User friend) async {
    try {
      final convo = await ref
          .read(chatRepositoryProvider)
          .createDirectConversation(friend.id);
      if (!mounted) return;
      setState(() => _isSelectingFriend = false);
      context.push('/chat/${convo.id}');
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Failed to start chat')));
    }
  }
}

/// A single avatar in the Messages "Friends" strip. Tapping it opens or
/// creates a DM. A green dot in the corner marks the friend as online.
class _FriendStripItem extends StatelessWidget {
  final User friend;
  final bool online;
  final VoidCallback onTap;
  const _FriendStripItem({
    required this.friend,
    required this.online,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final label = friend.displayName.trim().isNotEmpty
        ? friend.displayName.trim()
        : '@${friend.username}';

    return GestureDetector(
      onTap: onTap,
      child: SizedBox(
        width: 68,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 4),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Stack(
                children: [
                  CircleAvatar(
                    radius: 26,
                    backgroundColor: Colors.white10,
                    backgroundImage:
                        friend.hasAvatar ? NetworkImage(friend.avatarUrl) : null,
                    child: friend.hasAvatar
                        ? null
                        : Text(
                            label[0].toUpperCase(),
                            style: AppTextStyles.h3,
                          ),
                  ),
                  if (online)
                    Positioned(
                      right: 0,
                      bottom: 0,
                      child: Container(
                        width: 14,
                        height: 14,
                        decoration: BoxDecoration(
                          color: AppColors.onlineGreen,
                          shape: BoxShape.circle,
                          border: Border.all(
                            color: Colors.black,
                            width: 2.4,
                          ),
                        ),
                      ),
                    ),
                ],
              ),
              const SizedBox(height: 4),
              Text(
                label,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                textAlign: TextAlign.center,
                style: const TextStyle(
                  color: AppColors.textSecondary,
                  fontSize: 11,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _ConversationTile extends StatelessWidget {
  final dynamic convo;
  final String displayName;
  final VoidCallback onTap;
  const _ConversationTile({
    required this.convo,
    required this.displayName,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final initial = displayName.trim().isNotEmpty
        ? displayName.trim()[0].toUpperCase()
        : 'C';
    final preview = (convo.lastMessage?.toString().trim().isNotEmpty ?? false)
        ? convo.lastMessage.toString()
        : 'No messages';

    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(16),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            CircleAvatar(
              radius: 24,
              backgroundColor: Colors.white10,
              child: Text(initial),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(displayName, style: AppTextStyles.h3, maxLines: 1),
                  const SizedBox(height: 4),
                  Text(
                    preview,
                    style: AppTextStyles.bodySmall,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ),
            ),
            if (convo.unreadCount > 0)
              Container(
                padding: const EdgeInsets.all(6),
                decoration: const BoxDecoration(
                  color: AppColors.postbookPrimary,
                  shape: BoxShape.circle,
                ),
                child: Text(
                  '${convo.unreadCount}',
                  style: const TextStyle(color: Colors.white, fontSize: 10),
                ),
              ),
          ],
        ),
      ),
    );
  }
}
