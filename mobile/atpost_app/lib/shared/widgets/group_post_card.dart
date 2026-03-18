import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/group_post.dart';
import 'package:flutter/material.dart';

class GroupPostCard extends StatelessWidget {
  final GroupPost post;
  final VoidCallback? onTap;
  final VoidCallback? onSpark;
  final VoidCallback? onComment;
  final VoidCallback? onEcho;
  final VoidCallback? onStash;

  const GroupPostCard({
    super.key,
    required this.post,
    this.onTap,
    this.onSpark,
    this.onComment,
    this.onEcho,
    this.onStash,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Channel badge
            if (post.channelName != null && post.channelName!.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                  decoration: BoxDecoration(
                    color: AppColors.accentPurple.withValues(alpha: 0.15),
                    borderRadius: BorderRadius.circular(10),
                  ),
                  child: Text(
                    '#${post.channelName}',
                    style: AppTextStyles.labelSmall.copyWith(
                      color: AppColors.accentPurple,
                      fontSize: 11,
                    ),
                  ),
                ),
              ),

            // Announcement / Pinned badges
            if (post.isAnnouncement || post.isPinned)
              Padding(
                padding: const EdgeInsets.only(bottom: 6),
                child: Row(
                  children: [
                    if (post.isAnnouncement)
                      _Badge(
                        icon: Icons.campaign,
                        label: 'Announcement',
                        color: AppColors.statusWarning,
                      ),
                    if (post.isAnnouncement && post.isPinned)
                      const SizedBox(width: 6),
                    if (post.isPinned)
                      _Badge(
                        icon: Icons.push_pin,
                        label: 'Pinned',
                        color: AppColors.postbookPrimary,
                      ),
                  ],
                ),
              ),

            // Author row
            Row(
              children: [
                CircleAvatar(
                  radius: 16,
                  backgroundColor:
                      AppColors.postbookPrimary.withValues(alpha: 0.2),
                  backgroundImage: post.authorAvatarUrl != null
                      ? NetworkImage(post.authorAvatarUrl!)
                      : null,
                  child: post.authorAvatarUrl == null
                      ? Text(
                          (post.authorName ?? '?')
                              .substring(0, 1)
                              .toUpperCase(),
                          style: AppTextStyles.labelSmall.copyWith(
                            color: AppColors.postbookPrimary,
                          ),
                        )
                      : null,
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    post.authorName ?? 'Unknown',
                    style: AppTextStyles.label,
                  ),
                ),
                Text(
                  _timeAgo(post.createdAt),
                  style: AppTextStyles.labelSmall
                      .copyWith(color: AppColors.textMuted),
                ),
              ],
            ),

            // Title
            if (post.title != null && post.title!.isNotEmpty) ...[
              const SizedBox(height: 8),
              Text(
                post.title!,
                style: AppTextStyles.h3,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
            ],

            // Body
            if (post.body != null && post.body!.isNotEmpty) ...[
              const SizedBox(height: 6),
              Text(
                post.body!,
                style: AppTextStyles.body,
                maxLines: 6,
                overflow: TextOverflow.ellipsis,
              ),
            ],

            // Engagement rail
            const SizedBox(height: 10),
            Row(
              children: [
                _EngagementButton(
                  icon: Icons.bolt_outlined,
                  count: post.sparkCount,
                  label: 'Spark',
                  onTap: onSpark,
                ),
                const SizedBox(width: 16),
                _EngagementButton(
                  icon: Icons.chat_bubble_outline,
                  count: post.commentCount,
                  label: 'Comment',
                  onTap: onComment,
                ),
                const SizedBox(width: 16),
                _EngagementButton(
                  icon: Icons.repeat_rounded,
                  count: post.echoCount,
                  label: 'Echo',
                  onTap: onEcho,
                ),
                const Spacer(),
                _EngagementButton(
                  icon: Icons.bookmark_border_rounded,
                  count: 0,
                  label: 'Stash',
                  onTap: onStash,
                  showCount: false,
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  static String _timeAgo(DateTime date) {
    final diff = DateTime.now().difference(date);
    if (diff.inDays > 0) return '${diff.inDays}d';
    if (diff.inHours > 0) return '${diff.inHours}h';
    if (diff.inMinutes > 0) return '${diff.inMinutes}m';
    return 'now';
  }
}

class _Badge extends StatelessWidget {
  final IconData icon;
  final String label;
  final Color color;

  const _Badge({
    required this.icon,
    required this.label,
    required this.color,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 12, color: color),
          const SizedBox(width: 3),
          Text(
            label,
            style: AppTextStyles.labelSmall.copyWith(
              color: color,
              fontSize: 10,
            ),
          ),
        ],
      ),
    );
  }
}

class _EngagementButton extends StatelessWidget {
  final IconData icon;
  final int count;
  final String label;
  final VoidCallback? onTap;
  final bool showCount;

  const _EngagementButton({
    required this.icon,
    required this.count,
    required this.label,
    this.onTap,
    this.showCount = true,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      behavior: HitTestBehavior.opaque,
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 18, color: AppColors.textMuted),
          if (showCount && count > 0) ...[
            const SizedBox(width: 3),
            Text(
              _formatCount(count),
              style: AppTextStyles.labelSmall
                  .copyWith(color: AppColors.textMuted),
            ),
          ],
        ],
      ),
    );
  }

  static String _formatCount(int count) {
    if (count >= 1000000) return '${(count / 1000000).toStringAsFixed(1)}M';
    if (count >= 1000) return '${(count / 1000).toStringAsFixed(1)}K';
    return count.toString();
  }
}
