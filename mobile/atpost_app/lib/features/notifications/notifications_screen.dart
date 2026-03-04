import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/notification.dart';
import 'package:atpost_app/data/repositories/notification_repository.dart';
import 'package:atpost_app/providers/notification_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class NotificationsScreen extends ConsumerStatefulWidget {
  const NotificationsScreen({super.key});

  @override
  ConsumerState<NotificationsScreen> createState() => _NotificationsScreenState();
}

class _NotificationsScreenState extends ConsumerState<NotificationsScreen>
    with SingleTickerProviderStateMixin {
  late TabController _tabController;

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: 2, vsync: this);
  }

  @override
  void dispose() {
    _tabController.dispose();
    super.dispose();
  }

  String _typeIcon(String type) {
    switch (type) {
      case 'post.liked':
        return 'like';
      case 'post.commented':
        return 'comment';
      case 'user.followed':
        return 'follow';
      case 'user.mentioned':
        return 'mention';
      default:
        return 'default';
    }
  }

  IconData _iconData(String iconKey) {
    switch (iconKey) {
      case 'like':
        return Icons.thumb_up_rounded;
      case 'comment':
        return Icons.chat_bubble_rounded;
      case 'follow':
        return Icons.person_add_rounded;
      case 'mention':
        return Icons.alternate_email_rounded;
      default:
        return Icons.notifications_rounded;
    }
  }

  Color _iconColor(String iconKey) {
    switch (iconKey) {
      case 'like':
        return AppColors.postgramPrimary;
      case 'comment':
        return AppColors.posttubePrimary;
      case 'follow':
        return AppColors.postbookPrimary;
      case 'mention':
        return AppColors.accentPurple;
      default:
        return AppColors.textSecondary;
    }
  }

  String _typeText(AppNotification n) {
    switch (n.type) {
      case 'post.liked':
        return '${n.actorUserId} liked your post';
      case 'post.commented':
        return '${n.actorUserId} commented on your post';
      case 'user.followed':
        return '${n.actorUserId} started following you';
      case 'user.mentioned':
        return '${n.actorUserId} mentioned you';
      default:
        return n.type;
    }
  }

  String _relativeTime(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inSeconds < 60) return 'just now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    if (diff.inHours < 24) return '${diff.inHours}h ago';
    if (diff.inDays < 7) return '${diff.inDays}d ago';
    return '${(diff.inDays / 7).floor()}w ago';
  }

  Future<void> _handleTap(AppNotification n) async {
    final repo = ref.read(notificationRepositoryProvider);
    await repo.markRead(n.bucket, n.ts);
    ref.invalidate(notificationsProvider);
    if (n.deepLink != null && mounted) {
      context.push(n.deepLink!);
    }
  }

  Future<void> _markAllRead() async {
    final repo = ref.read(notificationRepositoryProvider);
    await repo.markAllRead();
    ref.invalidate(notificationsProvider);
  }

  Widget _buildNotificationTile(AppNotification n) {
    final iconKey = _typeIcon(n.type);
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 18, vertical: 4),
      tileColor: n.isRead ? Colors.transparent : AppColors.bgCard,
      leading: CircleAvatar(
        backgroundColor: _iconColor(iconKey).withValues(alpha: 0.15),
        child: Icon(
          _iconData(iconKey),
          color: _iconColor(iconKey),
          size: 20,
        ),
      ),
      title: Text(
        _typeText(n),
        style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
      ),
      subtitle: Text(
        _relativeTime(n.createdAt),
        style: AppTextStyles.labelSmall,
      ),
      onTap: () => _handleTap(n),
    );
  }

  Widget _buildList(List<AppNotification> items) {
    if (items.isEmpty) {
      return Center(
        child: Text(
          'No notifications yet',
          style: AppTextStyles.body.copyWith(color: AppColors.textDim),
        ),
      );
    }
    return ListView.separated(
      itemCount: items.length,
      separatorBuilder: (_, _) => Divider(
        height: 1,
        color: AppColors.borderSubtle,
      ),
      itemBuilder: (context, index) => _buildNotificationTile(items[index]),
    );
  }

  @override
  Widget build(BuildContext context) {
    final asyncNotifs = ref.watch(notificationsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Notifications', style: AppTextStyles.h2),
        actions: [
          IconButton(
            icon: const Icon(Icons.done_all_rounded, color: AppColors.postbookPrimary),
            tooltip: 'Mark all read',
            onPressed: _markAllRead,
          ),
        ],
        bottom: TabBar(
          controller: _tabController,
          labelStyle: AppTextStyles.label,
          unselectedLabelStyle: AppTextStyles.label.copyWith(color: AppColors.textDim),
          labelColor: AppColors.postbookPrimary,
          unselectedLabelColor: AppColors.textDim,
          indicatorColor: AppColors.postbookPrimary,
          tabs: const [
            Tab(text: 'All'),
            Tab(text: 'Unread'),
          ],
        ),
      ),
      body: RefreshIndicator(
        color: AppColors.postbookPrimary,
        onRefresh: () async {
          ref.invalidate(notificationsProvider);
        },
        child: asyncNotifs.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (err, _) => Center(
            child: Text(
              'Failed to load notifications',
              style: AppTextStyles.body.copyWith(color: AppColors.textDim),
            ),
          ),
          data: (items) => TabBarView(
            controller: _tabController,
            children: [
              _buildList(items),
              _buildList(items.where((n) => !n.isRead).toList()),
            ],
          ),
        ),
      ),
    );
  }
}
