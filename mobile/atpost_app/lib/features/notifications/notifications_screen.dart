import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
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
  ConsumerState<NotificationsScreen> createState() =>
      _NotificationsScreenState();
}

class _NotificationsScreenState extends ConsumerState<NotificationsScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabController;
  bool _markingAll = false;

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

  Future<void> _refresh() async {
    ref.invalidate(notificationsProvider);
    ref.invalidate(unreadNotificationCountProvider);
  }

  Future<void> _openNotification(AppNotification notification) async {
    try {
      if (!notification.isRead) {
        await ref
            .read(notificationRepositoryProvider)
            .markRead(notification.bucket, notification.ts);
        ref.invalidate(notificationsProvider);
        ref.invalidate(unreadNotificationCountProvider);
      }

      if (!mounted) return;
      if ((notification.deepLink ?? '').isNotEmpty) {
        context.push(notification.deepLink!);
      }
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not open notification.')),
      );
    }
  }

  Future<void> _markAllRead(List<AppNotification> items) async {
    if (_markingAll) return;
    if (!items.any((item) => !item.isRead)) return;

    setState(() => _markingAll = true);
    try {
      await ref.read(notificationRepositoryProvider).markAllRead();
      ref.invalidate(notificationsProvider);
      ref.invalidate(unreadNotificationCountProvider);
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not mark all as read.')),
      );
    } finally {
      if (mounted) {
        setState(() => _markingAll = false);
      }
    }
  }

  String _relativeTime(DateTime dateTime) {
    final diff = DateTime.now().difference(dateTime);
    if (diff.inSeconds < 45) return 'just now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m';
    if (diff.inHours < 24) return '${diff.inHours}h';
    if (diff.inDays < 7) return '${diff.inDays}d';
    return '${(diff.inDays / 7).floor()}w';
  }

  _NotifMeta _metaFor(AppNotification n) {
    switch (n.type) {
      case 'post.liked':
        return const _NotifMeta(
          icon: Icons.favorite_rounded,
          color: AppColors.postgramPrimary,
          text: 'liked your post',
        );
      case 'post.commented':
        return const _NotifMeta(
          icon: Icons.mode_comment_rounded,
          color: AppColors.posttubePrimary,
          text: 'commented on your post',
        );
      case 'user.followed':
        return const _NotifMeta(
          icon: Icons.person_add_alt_1_rounded,
          color: AppColors.postbookPrimary,
          text: 'started following you',
        );
      case 'user.mentioned':
        return const _NotifMeta(
          icon: Icons.alternate_email_rounded,
          color: AppColors.accentPurple,
          text: 'mentioned you',
        );
      default:
        return const _NotifMeta(
          icon: Icons.notifications_rounded,
          color: AppColors.textSecondary,
          text: 'sent a notification',
        );
    }
  }

  @override
  Widget build(BuildContext context) {
    final notificationsAsync = ref.watch(notificationsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: notificationsAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppColors.postbookPrimary),
          ),
          error: (_, _) => Center(
            child: _InlineStateCard(
              icon: Icons.notifications_off_outlined,
              message: 'Could not load notifications.',
              action: 'Retry',
              onTap: _refresh,
            ),
          ),
          data: (items) {
            final unread = items.where((item) => !item.isRead).toList();
            final all = items;

            return Column(
              children: [
                Padding(
                  padding: AppSpacing.pagePadding.copyWith(top: 10),
                  child: _HeaderCard(
                    totalCount: all.length,
                    unreadCount: unread.length,
                    markingAll: _markingAll,
                    onBack: () => context.pop(),
                    onRefresh: _refresh,
                    onMarkAllRead: () => _markAllRead(all),
                  ),
                ),
                const SizedBox(height: 12),
                Padding(
                  padding: AppSpacing.pagePadding,
                  child: Container(
                    decoration: BoxDecoration(
                      color: AppColors.bgCard,
                      borderRadius: BorderRadius.circular(
                        AppSpacing.radiusLarge,
                      ),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: TabBar(
                      controller: _tabController,
                      labelColor: AppColors.postbookPrimary,
                      unselectedLabelColor: AppColors.textDim,
                      indicatorColor: AppColors.postbookPrimary,
                      tabs: [
                        Tab(text: 'All (${all.length})'),
                        Tab(text: 'Unread (${unread.length})'),
                      ],
                    ),
                  ),
                ),
                Expanded(
                  child: RefreshIndicator(
                    color: AppColors.postbookPrimary,
                    onRefresh: _refresh,
                    child: TabBarView(
                      controller: _tabController,
                      children: [
                        _NotificationsList(
                          items: all,
                          emptyText: 'No notifications yet',
                          metaFor: _metaFor,
                          relativeTime: _relativeTime,
                          onTap: _openNotification,
                        ),
                        _NotificationsList(
                          items: unread,
                          emptyText: 'All caught up',
                          metaFor: _metaFor,
                          relativeTime: _relativeTime,
                          onTap: _openNotification,
                        ),
                      ],
                    ),
                  ),
                ),
              ],
            );
          },
        ),
      ),
    );
  }
}

class _HeaderCard extends StatelessWidget {
  const _HeaderCard({
    required this.totalCount,
    required this.unreadCount,
    required this.markingAll,
    required this.onBack,
    required this.onRefresh,
    required this.onMarkAllRead,
  });

  final int totalCount;
  final int unreadCount;
  final bool markingAll;
  final VoidCallback onBack;
  final VoidCallback onRefresh;
  final VoidCallback onMarkAllRead;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        gradient: const LinearGradient(
          colors: [Color(0x33FF6B35), Color(0x334ECDC4), Color(0x337B68EE)],
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
        ),
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderMedium),
      ),
      child: Column(
        children: [
          Row(
            children: [
              IconButton(
                onPressed: onBack,
                icon: const Icon(
                  Icons.arrow_back_ios_new_rounded,
                  size: 18,
                  color: AppColors.textPrimary,
                ),
              ),
              const SizedBox(width: 4),
              Expanded(
                child: Text(
                  'Notifications',
                  style: AppTextStyles.h1.copyWith(fontSize: 30),
                ),
              ),
              IconButton(
                onPressed: onRefresh,
                icon: const Icon(
                  Icons.refresh_rounded,
                  color: AppColors.textPrimary,
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              _Pill(label: 'Total', value: '$totalCount'),
              const SizedBox(width: 8),
              _Pill(label: 'Unread', value: '$unreadCount'),
              const Spacer(),
              ElevatedButton.icon(
                onPressed: unreadCount == 0 || markingAll
                    ? null
                    : onMarkAllRead,
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  foregroundColor: Colors.white,
                  disabledBackgroundColor: AppColors.bgTertiary,
                  disabledForegroundColor: AppColors.textDim,
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(10),
                  ),
                ),
                icon: markingAll
                    ? const SizedBox(
                        width: 12,
                        height: 12,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.white,
                        ),
                      )
                    : const Icon(Icons.done_all_rounded, size: 16),
                label: const Text('Read all'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _Pill extends StatelessWidget {
  const _Pill({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(value, style: AppTextStyles.h3),
          Text(label, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}

class _NotificationsList extends StatelessWidget {
  const _NotificationsList({
    required this.items,
    required this.emptyText,
    required this.metaFor,
    required this.relativeTime,
    required this.onTap,
  });

  final List<AppNotification> items;
  final String emptyText;
  final _NotifMeta Function(AppNotification) metaFor;
  final String Function(DateTime) relativeTime;
  final Future<void> Function(AppNotification) onTap;

  @override
  Widget build(BuildContext context) {
    if (items.isEmpty) {
      return ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        children: [
          SizedBox(
            height: 320,
            child: Center(
              child: _InlineStateCard(
                icon: Icons.mark_email_read_outlined,
                message: emptyText,
                action: 'Refresh',
                onTap: () {},
              ),
            ),
          ),
        ],
      );
    }

    return ListView.separated(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: AppSpacing.pagePadding.copyWith(top: 6, bottom: 110),
      itemCount: items.length,
      separatorBuilder: (_, _) => const SizedBox(height: 8),
      itemBuilder: (context, index) {
        final notification = items[index];
        final meta = metaFor(notification);

        return Material(
          color: Colors.transparent,
          child: InkWell(
            onTap: () => onTap(notification),
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            child: Ink(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: notification.isRead
                    ? AppColors.bgCard
                    : AppColors.postbookPrimary.withValues(alpha: 0.08),
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                border: Border.all(
                  color: notification.isRead
                      ? AppColors.borderSubtle
                      : AppColors.postbookPrimary.withValues(alpha: 0.4),
                ),
              ),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Container(
                    width: 38,
                    height: 38,
                    decoration: BoxDecoration(
                      color: meta.color.withValues(alpha: 0.2),
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: Icon(meta.icon, color: meta.color, size: 20),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        RichText(
                          text: TextSpan(
                            style: AppTextStyles.body.copyWith(
                              color: AppColors.textSecondary,
                            ),
                            children: [
                              TextSpan(
                                text: notification.actorUserId,
                                style: AppTextStyles.label.copyWith(
                                  color: AppColors.textPrimary,
                                ),
                              ),
                              TextSpan(text: ' ${meta.text}'),
                            ],
                          ),
                        ),
                        const SizedBox(height: 4),
                        Row(
                          children: [
                            Text(
                              relativeTime(notification.createdAt),
                              style: AppTextStyles.labelSmall,
                            ),
                            if (!notification.isRead) ...[
                              const SizedBox(width: 8),
                              Container(
                                width: 7,
                                height: 7,
                                decoration: const BoxDecoration(
                                  color: AppColors.postbookPrimary,
                                  shape: BoxShape.circle,
                                ),
                              ),
                            ],
                          ],
                        ),
                      ],
                    ),
                  ),
                  const Icon(
                    Icons.chevron_right_rounded,
                    color: AppColors.textMuted,
                  ),
                ],
              ),
            ),
          ),
        );
      },
    );
  }
}

class _NotifMeta {
  const _NotifMeta({
    required this.icon,
    required this.color,
    required this.text,
  });

  final IconData icon;
  final Color color;
  final String text;
}

class _InlineStateCard extends StatelessWidget {
  const _InlineStateCard({
    required this.icon,
    required this.message,
    required this.action,
    required this.onTap,
  });

  final IconData icon;
  final String message;
  final String action;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: AppSpacing.pagePadding,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Icon(icon, color: AppColors.textMuted),
          const SizedBox(width: 10),
          Expanded(child: Text(message, style: AppTextStyles.bodySmall)),
          TextButton(
            onPressed: onTap,
            child: Text(
              action,
              style: AppTextStyles.label.copyWith(
                color: AppColors.postbookPrimary,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
