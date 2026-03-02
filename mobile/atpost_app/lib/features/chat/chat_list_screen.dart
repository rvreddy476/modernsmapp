import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/shared/widgets/filter_pills.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';
import 'package:go_router/go_router.dart';

class ChatListScreen extends StatefulWidget {
  const ChatListScreen({super.key});

  @override
  State<ChatListScreen> createState() => _ChatListScreenState();
}

class _ChatListScreenState extends State<ChatListScreen> {
  final TextEditingController _searchController = TextEditingController();
  int _activeFilter = 0;
  bool _showSearch = false;

  static const List<_OnlineUser> _onlineUsers = [
    _OnlineUser(id: 'aarav', initials: 'AS'),
    _OnlineUser(id: 'meera', initials: 'MD'),
    _OnlineUser(id: 'neha', initials: 'NM'),
    _OnlineUser(id: 'ryan', initials: 'RP'),
    _OnlineUser(id: 'tara', initials: 'TS'),
    _OnlineUser(id: 'vani', initials: 'VK'),
  ];

  static const List<_Conversation> _conversations = [
    _Conversation(
      id: 'aarav-singh',
      name: 'Aarav Singh',
      subtitle: 'Can we ship the home feed pass tonight?',
      time: '10:24',
      unreadCount: 3,
      isOnline: true,
    ),
    _Conversation(
      id: 'design-room',
      name: 'Design Room',
      subtitle: '5 members',
      time: '09:58',
      unreadCount: 12,
      isGroup: true,
      isPinned: true,
    ),
    _Conversation(
      id: 'neha-motion',
      name: 'Neha Motion',
      subtitle: 'typing',
      time: '09:42',
      isTyping: true,
      isOnline: true,
    ),
    _Conversation(
      id: 'ops-updates',
      name: 'Ops Updates',
      subtitle: 'Deployment complete on staging',
      time: '08:13',
      isGroup: true,
      isArchived: true,
    ),
    _Conversation(
      id: 'meera-das',
      name: 'Meera Das',
      subtitle: 'The chapter markers look clean.',
      time: 'Yesterday',
      isOnline: true,
    ),
    _Conversation(
      id: 'growth',
      name: 'Growth Team',
      subtitle: 'Drop metrics before standup',
      time: 'Yesterday',
      unreadCount: 2,
      isGroup: true,
    ),
  ];

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final conversations = _filteredConversations();

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
                      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
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
              Text('Online now', style: AppTextStyles.h3),
              const SizedBox(height: 10),
              SizedBox(
                height: 76,
                child: ListView.separated(
                  scrollDirection: Axis.horizontal,
                  itemCount: _onlineUsers.length,
                  separatorBuilder: (_, _) => const SizedBox(width: 10),
                  itemBuilder: (context, index) {
                    final user = _onlineUsers[index];
                    return Column(
                      children: [
                        Stack(
                          clipBehavior: Clip.none,
                          children: [
                            Container(
                              width: 52,
                              height: 52,
                              decoration: BoxDecoration(
                                color: AppColors.bgTertiary,
                                borderRadius: BorderRadius.circular(16),
                                border: Border.all(color: AppColors.borderSubtle),
                              ),
                              child: Center(
                                child: Text(user.initials, style: AppTextStyles.label),
                              ),
                            ),
                            Positioned(
                              right: -2,
                              bottom: -2,
                              child: Container(
                                width: 14,
                                height: 14,
                                decoration: BoxDecoration(
                                  color: AppColors.onlineGreen,
                                  shape: BoxShape.circle,
                                  border: Border.all(color: AppColors.bgPrimary, width: 2),
                                ),
                              )
                                  .animate(onPlay: (controller) => controller.repeat())
                                  .fade(begin: 0.55, end: 1, duration: 1100.ms),
                            ),
                          ],
                        ),
                        const SizedBox(height: 6),
                        SizedBox(
                          width: 56,
                          child: Text(
                            user.id,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            textAlign: TextAlign.center,
                            style: AppTextStyles.labelSmall,
                          ),
                        ),
                      ],
                    );
                  },
                ),
              ),
              const SizedBox(height: 14),
              Expanded(
                child: ListView.separated(
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
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  List<_Conversation> _filteredConversations() {
    final query = _searchController.text.trim().toLowerCase();

    return _conversations.where((conversation) {
      final byFilter = switch (_activeFilter) {
        0 => true,
        1 => conversation.unreadCount > 0,
        2 => conversation.isGroup,
        3 => conversation.isArchived,
        _ => true,
      };
      if (!byFilter) {
        return false;
      }
      if (query.isEmpty) {
        return true;
      }
      return conversation.name.toLowerCase().contains(query) ||
          conversation.subtitle.toLowerCase().contains(query);
    }).toList();
  }
}

class _ConversationTile extends StatelessWidget {
  const _ConversationTile({
    required this.conversation,
    required this.onTap,
  });

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
            Stack(
              clipBehavior: Clip.none,
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
                if (conversation.isOnline)
                  Positioned(
                    right: -2,
                    bottom: -2,
                    child: Container(
                      width: 12,
                      height: 12,
                      decoration: BoxDecoration(
                        color: AppColors.onlineGreen,
                        shape: BoxShape.circle,
                        border: Border.all(color: AppColors.bgPrimary, width: 2),
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
                      Expanded(
                        child: Row(
                          children: [
                            if (conversation.isPinned) ...[
                              const Icon(
                                Icons.push_pin,
                                size: 14,
                                color: AppColors.accentPurple,
                              ),
                              const SizedBox(width: 4),
                            ],
                            Flexible(
                              child: Text(
                                conversation.name,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: AppTextStyles.h3,
                              ),
                            ),
                          ],
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
                  if (conversation.isTyping)
                    Row(
                      children: [
                        Text(
                          'typing',
                          style: AppTextStyles.bodySmall.copyWith(
                            color: AppColors.posttubePrimary,
                          ),
                        ),
                        const SizedBox(width: 6),
                        const _TypingDots(size: 5),
                      ],
                    )
                  else
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
                    style: AppTextStyles.labelTiny.copyWith(color: Colors.white),
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
    final parts = value.split(' ').where((segment) => segment.isNotEmpty).toList();
    if (parts.length == 1) {
      return parts.first.substring(0, 1).toUpperCase();
    }
    return '${parts[0][0]}${parts[1][0]}'.toUpperCase();
  }
}

class _TypingDots extends StatelessWidget {
  const _TypingDots({this.size = 6});

  final double size;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: List.generate(3, (index) {
        return Padding(
          padding: const EdgeInsets.only(right: 3),
          child: Container(
            width: size,
            height: size,
            decoration: const BoxDecoration(
              color: AppColors.onlineGreen,
              shape: BoxShape.circle,
            ),
          )
              .animate(onPlay: (controller) => controller.repeat())
              .moveY(
                begin: 0,
                end: -4,
                duration: 420.ms,
                delay: (index * 150).ms,
                curve: Curves.easeOut,
              )
              .moveY(
                begin: -4,
                end: 0,
                duration: 420.ms,
                curve: Curves.easeIn,
              ),
        );
      }),
    );
  }
}

class _OnlineUser {
  const _OnlineUser({
    required this.id,
    required this.initials,
  });

  final String id;
  final String initials;
}

class _Conversation {
  const _Conversation({
    required this.id,
    required this.name,
    required this.subtitle,
    required this.time,
    this.unreadCount = 0,
    this.isOnline = false,
    this.isTyping = false,
    this.isPinned = false,
    this.isGroup = false,
    this.isArchived = false,
  });

  final String id;
  final String name;
  final String subtitle;
  final String time;
  final int unreadCount;
  final bool isOnline;
  final bool isTyping;
  final bool isPinned;
  final bool isGroup;
  final bool isArchived;
}
