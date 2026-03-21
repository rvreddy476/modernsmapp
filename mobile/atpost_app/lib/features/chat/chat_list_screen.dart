import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/conversation.dart';
import 'package:atpost_app/providers/chat_provider.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/shared/widgets/filter_pills.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ChatListScreen extends ConsumerStatefulWidget {
  const ChatListScreen({super.key});

  @override
  ConsumerState<ChatListScreen> createState() => _ChatListScreenState();
}

class _ChatListScreenState extends ConsumerState<ChatListScreen> {
  final TextEditingController _searchController = TextEditingController();
  bool _showSearch = false;

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
    final conversations = ref.watch(filteredConversationsProvider);
    final conversationsLoading = ref.watch(chatConversationsProvider).isLoading;
    final activeFilter = ref.watch(chatActiveFilterProvider);
    final currentUserId = ref.watch(authServiceProvider).userId;

    return Scaffold(
      floatingActionButton: _buildNewChatButton(),
      body: SafeArea(
        child: Padding(
          padding: AppSpacing.pagePadding.copyWith(top: 12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _buildHeader(context),
              _buildSearchField(),
              const SizedBox(height: 14),
              FilterPills(
                labels: const ['All', 'Unread', 'Groups', 'Archived'],
                activeIndex: activeFilter,
                onChanged: (index) {
                  ref.read(chatActiveFilterProvider.notifier).state = index;
                },
              ),
              const SizedBox(height: 16),
              Expanded(
                child: _buildConversationList(
                  conversations,
                  conversationsLoading,
                  currentUserId,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildHeader(BuildContext context) {
    return Row(
      children: [
        GlassIconButton(
          icon: Icons.arrow_back_ios_new,
          tooltip: 'Back',
          onPressed: () => context.pop(),
        ),
        const SizedBox(width: 8),
        Text('Messages', style: AppTextStyles.h1),
        const Spacer(),
        GlassIconButton(
          icon: Icons.search,
          tooltip: 'Search',
          onPressed: () => setState(() => _showSearch = !_showSearch),
        ),
        const SizedBox(width: 8),
        const GlassIconButton(icon: Icons.tune, tooltip: 'Filter'),
      ],
    );
  }

  Widget _buildSearchField() {
    return AnimatedCrossFade(
      firstChild: const SizedBox(height: 0),
      secondChild: Padding(
        padding: const EdgeInsets.only(top: 12),
        child: Container(
          decoration: BoxDecoration(
            color: AppColors.glassBg,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.glassBorder),
          ),
          child: TextField(
            controller: _searchController,
            style: AppTextStyles.body,
            decoration: InputDecoration(
              border: InputBorder.none,
              contentPadding: const EdgeInsets.symmetric(
                horizontal: 14,
                vertical: 12,
              ),
              hintText: 'Search messages',
              hintStyle: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textGhost,
              ),
              prefixIcon: const Icon(
                Icons.search,
                size: 18,
                color: AppColors.textMuted,
              ),
            ),
          ),
        ),
      ),
      crossFadeState: _showSearch
          ? CrossFadeState.showSecond
          : CrossFadeState.showFirst,
      duration: const Duration(milliseconds: 220),
    );
  }

  Widget _buildConversationList(
    List<Conversation> conversations,
    bool isLoading,
    String? currentUserId,
  ) {
    if (isLoading && conversations.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }

    if (conversations.isEmpty) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(
              Icons.chat_bubble_outline,
              color: AppColors.textMuted,
              size: 32,
            ),
            const SizedBox(height: 8),
            Text('No messages found', style: AppTextStyles.bodySmall),
          ],
        ),
      );
    }

    return ListView.separated(
      itemCount: conversations.length,
      separatorBuilder: (_, __) => const SizedBox(height: 10),
      padding: const EdgeInsets.only(bottom: 90),
      itemBuilder: (context, index) {
        final convo = conversations[index];
        return _ConversationTile(
          conversation: convo,
          currentUserId: currentUserId,
          onTap: () => context.push('/chat/${convo.id}'),
        );
      },
    );
  }

  Widget _buildNewChatButton() {
    return GestureDetector(
      onTap: () {},
      child: Container(
        width: 56,
        height: 56,
        decoration: BoxDecoration(
          gradient: AppColors.ctaGradient,
          borderRadius: BorderRadius.circular(18),
          boxShadow: const [
            BoxShadow(
              color: Color(0x66FF6B35),
              blurRadius: 16,
              offset: Offset(0, 6),
            ),
          ],
        ),
        child: const Icon(Icons.edit, color: Colors.white),
      ),
    );
  }
}

class _ConversationTile extends StatelessWidget {
  const _ConversationTile({
    required this.conversation,
    required this.currentUserId,
    required this.onTap,
  });

  final Conversation conversation;
  final String? currentUserId;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return RepaintBoundary(
      child: GestureDetector(
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(
              color: conversation.unreadCount > 0
                  ? AppColors.postbookPrimary.withOpacity(0.28)
                  : AppColors.borderSubtle,
            ),
          ),
          child: Row(
            children: [
              _buildAvatar(),
              const SizedBox(width: 12),
              _buildInfo(),
              if (conversation.unreadCount > 0) _buildUnreadBadge(),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildAvatar() {
    final isGroup = conversation.type == 'group';
    final displayName = conversation.displayNameFor(currentUserId);
    return Container(
      width: 52,
      height: 52,
      decoration: BoxDecoration(
        gradient: isGroup
            ? AppColors.posttubeGradient
            : AppColors.postbookGradient,
        borderRadius: BorderRadius.circular(16),
      ),
      child: Center(
        child: Text(
          _initials(displayName),
          style: AppTextStyles.label.copyWith(color: Colors.white),
        ),
      ),
    );
  }

  Widget _buildInfo() {
    final displayName = conversation.displayNameFor(currentUserId);
    return Expanded(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  displayName,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyles.h3,
                ),
              ),
              const SizedBox(width: 8),
              Text(
                _formatTime(conversation.lastMessageAt),
                style: AppTextStyles.monoSmall.copyWith(
                  color: AppColors.textDim,
                ),
              ),
            ],
          ),
          const SizedBox(height: 5),
          Text(
            conversation.lastMessage ?? '',
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.textSecondary,
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildUnreadBadge() {
    return Container(
      constraints: const BoxConstraints(minWidth: 20),
      height: 20,
      margin: const EdgeInsets.only(left: 10),
      padding: const EdgeInsets.symmetric(horizontal: 6),
      decoration: BoxDecoration(
        color: AppColors.postbookPrimary,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Center(
        child: Text(
          '${conversation.unreadCount}',
          style: AppTextStyles.labelTiny.copyWith(color: Colors.white),
        ),
      ),
    );
  }

  String _formatTime(DateTime? dt) {
    if (dt == null) return '';
    final now = DateTime.now();
    final diff = now.difference(dt);
    if (diff.inDays >= 1) return 'Yesterday';
    return '${dt.hour.toString().padLeft(2, '0')}:${dt.minute.toString().padLeft(2, '0')}';
  }

  String _initials(String value) {
    final parts = value.split(' ').where((segment) => segment.isNotEmpty).toList();
    if (parts.isEmpty) return '?';
    if (parts.length == 1) return parts.first.substring(0, 1).toUpperCase();
    return '${parts[0][0]}${parts[1][0]}'.toUpperCase();
  }
}
