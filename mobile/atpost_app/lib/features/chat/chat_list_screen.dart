import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/conversation.dart';
import 'package:atpost_app/providers/chat_provider.dart';
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
  int _activeFilter = 0;
  bool _showSearch = false;

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final conversationsAsync = ref.watch(chatConversationsProvider);

    return Scaffold(
      floatingActionButton: GestureDetector(
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
      ),
      body: SafeArea(
        child: Padding(
          padding: AppSpacing.pagePadding.copyWith(top: 12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  GlassIconButton(
                    icon: Icons.arrow_back_ios_new,
                    onPressed: () => context.pop(),
                  ),
                  const SizedBox(width: 8),
                  Text('Messages', style: AppTextStyles.h1),
                  const Spacer(),
                  GlassIconButton(
                    icon: Icons.search,
                    onPressed: () => setState(() => _showSearch = !_showSearch),
                  ),
                  const SizedBox(width: 8),
                  const GlassIconButton(icon: Icons.tune),
                ],
              ),
              AnimatedCrossFade(
                firstChild: const SizedBox(height: 0),
                secondChild: Padding(
                  padding: const EdgeInsets.only(top: 12),
                  child: Container(
                    decoration: BoxDecoration(
                      color: AppColors.glassBg,
                      borderRadius: BorderRadius.circular(
                        AppSpacing.radiusLarge,
                      ),
                      border: Border.all(color: AppColors.glassBorder),
                    ),
                    child: TextField(
                      controller: _searchController,
                      onChanged: (_) => setState(() {}),
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
              ),
              const SizedBox(height: 14),
              FilterPills(
                labels: const ['All', 'Unread', 'Groups', 'Archived'],
                activeIndex: _activeFilter,
                onChanged: (index) => setState(() => _activeFilter = index),
              ),
              const SizedBox(height: 16),
              Expanded(
                child: conversationsAsync.when(
                  loading: () =>
                      const Center(child: CircularProgressIndicator()),
                  error: (_, _) => Center(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const Icon(
                          Icons.chat_bubble_outline,
                          color: AppColors.textMuted,
                          size: 32,
                        ),
                        const SizedBox(height: 8),
                        Text(
                          'Could not load messages',
                          style: AppTextStyles.bodySmall,
                        ),
                      ],
                    ),
                  ),
                  data: (apiConversations) {
                    final conversations = _filterApiConversations(
                      apiConversations,
                    );
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
                            Text(
                              'No conversations yet',
                              style: AppTextStyles.bodySmall,
                            ),
                          ],
                        ),
                      );
                    }
                    return ListView.separated(
                      itemCount: conversations.length,
                      separatorBuilder: (_, _) => const SizedBox(height: 10),
                      padding: const EdgeInsets.only(bottom: 90),
                      itemBuilder: (context, index) {
                        final convo = conversations[index];
                        return _ConversationTile(
                          conversation: convo,
                          onTap: () => context.push('/chat/${convo.id}'),
                        );
                      },
                    );
                  },
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  List<_Conversation> _filterApiConversations(List<Conversation> apiList) {
    final query = _searchController.text.trim().toLowerCase();
    return apiList
        .map(
          (c) => _Conversation(
            id: c.id,
            name: c.name ?? 'Direct Message',
            subtitle: c.lastMessage ?? '',
            time: c.lastMessageAt != null
                ? _formatConvoTime(c.lastMessageAt!)
                : '',
            unreadCount: c.unreadCount,
            isGroup: c.type == 'group',
            isArchived: false,
          ),
        )
        .where((c) {
          final byFilter = switch (_activeFilter) {
            0 => true,
            1 => c.unreadCount > 0,
            2 => c.isGroup,
            3 => c.isArchived,
            _ => true,
          };
          if (!byFilter) return false;
          if (query.isEmpty) return true;
          return c.name.toLowerCase().contains(query) ||
              c.subtitle.toLowerCase().contains(query);
        })
        .toList();
  }

  String _formatConvoTime(DateTime dt) {
    final now = DateTime.now();
    final diff = now.difference(dt);
    if (diff.inDays >= 1) return 'Yesterday';
    return '${dt.hour.toString().padLeft(2, '0')}:${dt.minute.toString().padLeft(2, '0')}';
  }
}

class _ConversationTile extends StatelessWidget {
  const _ConversationTile({required this.conversation, required this.onTap});

  final _Conversation conversation;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(
            color: conversation.unreadCount > 0
                ? AppColors.postbookPrimary.withValues(alpha: 0.28)
                : AppColors.borderSubtle,
          ),
        ),
        child: Row(
          children: [
            Container(
              width: 52,
              height: 52,
              decoration: BoxDecoration(
                gradient: conversation.isGroup
                    ? AppColors.posttubeGradient
                    : AppColors.postbookGradient,
                borderRadius: BorderRadius.circular(16),
              ),
              child: Center(
                child: Text(
                  _initials(conversation.name),
                  style: AppTextStyles.label.copyWith(color: Colors.white),
                ),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          conversation.name,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: AppTextStyles.h3,
                        ),
                      ),
                      const SizedBox(width: 8),
                      Text(
                        conversation.time,
                        style: AppTextStyles.monoSmall.copyWith(
                          color: AppColors.textDim,
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 5),
                  Text(
                    conversation.subtitle,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyles.bodySmall.copyWith(
                      color: AppColors.textSecondary,
                    ),
                  ),
                ],
              ),
            ),
            if (conversation.unreadCount > 0) ...[
              const SizedBox(width: 10),
              Container(
                constraints: const BoxConstraints(minWidth: 20),
                height: 20,
                padding: const EdgeInsets.symmetric(horizontal: 6),
                decoration: BoxDecoration(
                  color: AppColors.postbookPrimary,
                  borderRadius: BorderRadius.circular(999),
                ),
                child: Center(
                  child: Text(
                    '${conversation.unreadCount}',
                    style: AppTextStyles.labelTiny.copyWith(
                      color: Colors.white,
                    ),
                  ),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }

  String _initials(String value) {
    final parts = value
        .split(' ')
        .where((segment) => segment.isNotEmpty)
        .toList();
    if (parts.length == 1) {
      return parts.first.substring(0, 1).toUpperCase();
    }
    return '${parts[0][0]}${parts[1][0]}'.toUpperCase();
  }
}

class _Conversation {
  const _Conversation({
    required this.id,
    required this.name,
    required this.subtitle,
    required this.time,
    this.unreadCount = 0,
    this.isGroup = false,
    this.isArchived = false,
  });

  final String id;
  final String name;
  final String subtitle;
  final String time;
  final int unreadCount;
  final bool isGroup;
  final bool isArchived;
}
