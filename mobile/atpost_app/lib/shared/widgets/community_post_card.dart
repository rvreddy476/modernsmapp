import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/community_post.dart';
import 'package:flutter/material.dart';

class CommunityPostCard extends StatelessWidget {
  final CommunityPost post;
  final VoidCallback? onTap;
  final VoidCallback? onSpark;
  final VoidCallback? onComment;

  const CommunityPostCard({
    super.key,
    required this.post,
    this.onTap,
    this.onSpark,
    this.onComment,
  });

  @override
  Widget build(BuildContext context) {
    final indent = (post.threadDepth * 16.0).clamp(0.0, 64.0);

    return GestureDetector(
      onTap: onTap,
      child: Padding(
        padding: EdgeInsets.only(left: indent),
        child: Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(
              color: post.threadDepth > 0
                  ? AppColors.accentPurple.withValues(alpha: 0.2)
                  : AppColors.borderSubtle,
            ),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // Space badge + Q&A badges
              Row(
                children: [
                  if (post.spaceName != null && post.spaceName!.isNotEmpty)
                    Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 8, vertical: 2),
                      decoration: BoxDecoration(
                        color: AppColors.posttubePrimary
                            .withValues(alpha: 0.15),
                        borderRadius: BorderRadius.circular(10),
                      ),
                      child: Text(
                        post.spaceName!,
                        style: AppTextStyles.labelSmall.copyWith(
                          color: AppColors.posttubePrimary,
                          fontSize: 11,
                        ),
                      ),
                    ),
                  const Spacer(),
                  if (post.isAnswered)
                    _QaBadge(
                      icon: Icons.check_circle,
                      label: 'Accepted',
                      color: AppColors.statusSuccess,
                    ),
                  if (post.isExpertAnswer) ...[
                    if (post.isAnswered) const SizedBox(width: 6),
                    _QaBadge(
                      icon: Icons.school,
                      label: 'Expert',
                      color: AppColors.accentPurple,
                    ),
                  ],
                  if (post.isPinned) ...[
                    const SizedBox(width: 6),
                    Icon(Icons.push_pin,
                        size: 14, color: AppColors.postbookPrimary),
                  ],
                  if (post.isFeatured) ...[
                    const SizedBox(width: 6),
                    Icon(Icons.star,
                        size: 14, color: AppColors.statusWarning),
                  ],
                ],
              ),

              const SizedBox(height: 8),

              // Author row
              Row(
                children: [
                  CircleAvatar(
                    radius: 14,
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
                              fontSize: 11,
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
                const SizedBox(height: 4),
                Text(
                  post.body!,
                  style: AppTextStyles.body,
                  maxLines: 5,
                  overflow: TextOverflow.ellipsis,
                ),
              ],

              // Engagement rail
              const SizedBox(height: 10),
              Row(
                children: [
                  _EngagementChip(
                    icon: Icons.bolt_outlined,
                    count: post.sparkCount,
                    onTap: onSpark,
                  ),
                  const SizedBox(width: 16),
                  _EngagementChip(
                    icon: Icons.chat_bubble_outline,
                    count: post.commentCount,
                    onTap: onComment,
                  ),
                  if (post.replyCount > 0) ...[
                    const SizedBox(width: 16),
                    _EngagementChip(
                      icon: Icons.reply_rounded,
                      count: post.replyCount,
                    ),
                  ],
                  const Spacer(),
                  Text(
                    '${post.viewCount} views',
                    style: AppTextStyles.labelSmall
                        .copyWith(color: AppColors.textDim, fontSize: 10),
                  ),
                ],
              ),
            ],
          ),
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

class _QaBadge extends StatelessWidget {
  final IconData icon;
  final String label;
  final Color color;

  const _QaBadge({
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

class _EngagementChip extends StatelessWidget {
  final IconData icon;
  final int count;
  final VoidCallback? onTap;

  const _EngagementChip({
    required this.icon,
    required this.count,
    this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      behavior: HitTestBehavior.opaque,
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 16, color: AppColors.textMuted),
          if (count > 0) ...[
            const SizedBox(width: 3),
            Text(
              count.toString(),
              style: AppTextStyles.labelSmall
                  .copyWith(color: AppColors.textMuted),
            ),
          ],
        ],
      ),
    );
  }
}
