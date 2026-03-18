import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:atpost_app/shared/widgets/reaction_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ActionPillButton extends StatelessWidget {
  const ActionPillButton({
    super.key,
    required this.icon,
    required this.label,
    this.active = false,
    this.onTap,
    this.onLongPress,
  });

  final IconData icon;
  final String label;
  final bool active;
  final VoidCallback? onTap;
  final VoidCallback? onLongPress;

  @override
  Widget build(BuildContext context) {
    final color = active ? AppColors.postbookPrimary : AppColors.textMuted;
    return Semantics(
      button: true,
      label: '$label${active ? ", selected" : ""}',
      child: Tooltip(
        message: label,
        child: GestureDetector(
          onTap: onTap,
          onLongPress: onLongPress,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
            decoration: BoxDecoration(
              color: Colors.white.withValues(alpha: 0.05),
              borderRadius: BorderRadius.circular(10),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                ExcludeSemantics(child: Icon(icon, size: 15, color: color)),
                const SizedBox(width: 6),
                ExcludeSemantics(
                  child: Text(label, style: AppTextStyles.label.copyWith(color: color)),
                ),
              ],
            ),
          )
              .animate()
              .scale(duration: 100.ms, begin: const Offset(1, 1), end: const Offset(1.02, 1.02)),
        ),
      ),
    );
  }
}

class PostCard extends ConsumerWidget {
  const PostCard({
    super.key,
    required this.post,
  });

  final Post post;

  String _formatCount(int count) {
    if (count >= 1000000) return '${(count / 1000000).toStringAsFixed(1)}M';
    if (count >= 1000) return '${(count / 1000).toStringAsFixed(1)}K';
    return count.toString();
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final repo = ref.watch(postRepositoryProvider);

    return Semantics(
      label: 'Post by ${post.authorName ?? "Anonymous"}',
      child: Container(
        margin: const EdgeInsets.only(bottom: 14),
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Semantics(
                  image: true,
                  label: 'Profile photo of ${post.authorName ?? "user"}',
                  child: Container(
                    width: 40,
                    height: 40,
                    decoration: BoxDecoration(
                      color: AppColors.bgTertiary,
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: Center(
                      child: post.authorAvatar != null
                          ? ClipRRect(
                              borderRadius: BorderRadius.circular(12),
                              child: Image.network(
                                post.authorAvatar!,
                                semanticLabel: 'Profile photo of ${post.authorName ?? "user"}',
                              ),
                            )
                          : ExcludeSemantics(
                              child: Text(post.authorName?.substring(0, 1) ?? '?', style: AppTextStyles.h3),
                            ),
                    ),
                  ),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(post.authorName ?? 'Anonymous', style: AppTextStyles.h3),
                      Text(
                        '@${(post.authorName ?? 'user').toLowerCase().replaceAll(' ', '_')} \u2022 ${_timeAgo(post.createdAt)}',
                        style: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
                      ),
                    ],
                  ),
                ),
                Semantics(
                  button: true,
                  label: 'More options',
                  child: const Icon(Icons.more_horiz, color: AppColors.textMuted),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Text(post.content, style: AppTextStyles.body),
            if (post.tags.isNotEmpty) ...[
              const SizedBox(height: 10),
              Wrap(
                spacing: 8,
                runSpacing: 6,
                children: post.tags
                    .map(
                      (tag) => Container(
                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 5),
                        decoration: BoxDecoration(
                          color: AppColors.bgCard,
                          borderRadius: BorderRadius.circular(999),
                        ),
                        child: Text(tag, style: AppTextStyles.tag),
                      ),
                    )
                    .toList(),
              ),
            ],
            const SizedBox(height: 12),
            Row(
              children: [
                Builder(
                  builder: (context) => ActionPillButton(
                    icon: post.isLiked ? Icons.favorite : Icons.favorite_border,
                    label: _formatCount(post.likeCount),
                    active: post.isLiked,
                    onTap: () => repo.toggleReaction(post.id),
                    onLongPress: () {
                      final RenderBox box = context.findRenderObject() as RenderBox;
                      final position = box.localToGlobal(Offset.zero);
                      showReactionPicker(context, position, (emoji) {
                        repo.toggleReaction(post.id, emoji: emoji);
                      });
                    },
                  ),
                ),
                const SizedBox(width: 8),
                ActionPillButton(
                  icon: Icons.chat_bubble_outline,
                  label: _formatCount(post.commentCount),
                  onTap: () {
                    // Navigate to comments
                  },
                ),
                const SizedBox(width: 8),
                ActionPillButton(
                  icon: Icons.reply,
                  label: 'Share',
                  onTap: () {
                    // Share post
                  },
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  String _timeAgo(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inDays > 0) return '${diff.inDays}d';
    if (diff.inHours > 0) return '${diff.inHours}h';
    if (diff.inMinutes > 0) return '${diff.inMinutes}m';
    return 'now';
  }
}

class ReelCard extends StatelessWidget {
  const ReelCard({
    super.key,
    required this.title,
    required this.creator,
    required this.duration,
    this.onTap,
  });

  final String title;
  final String creator;
  final String duration;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      label: 'Reel: $title by $creator, $duration',
      child: GestureDetector(
        onTap: onTap,
        child: Container(
          margin: const EdgeInsets.only(bottom: 14),
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppColors.accentPurple.withValues(alpha: 0.08),
            borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
            border: Border.all(color: AppColors.accentPurple.withValues(alpha: 0.2)),
          ),
          child: Row(
            children: [
              Container(
                width: 100,
                height: 140,
                decoration: BoxDecoration(
                  gradient: const LinearGradient(
                    begin: Alignment.topCenter,
                    end: Alignment.bottomCenter,
                    colors: [Color(0xFF2A1F3B), Color(0xFF161620)],
                  ),
                  borderRadius: BorderRadius.circular(14),
                ),
                child: Stack(
                  children: [
                    Positioned(
                      top: 8,
                      left: 8,
                      child: Container(
                        padding:
                            const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                        decoration: BoxDecoration(
                          color: AppColors.postgramPrimary.withValues(alpha: 0.2),
                          borderRadius: BorderRadius.circular(999),
                        ),
                        child: Text(
                          'POSTGRAM',
                          style: AppTextStyles.labelTiny
                              .copyWith(color: AppColors.postgramPrimary),
                        ),
                      ),
                    ),
                    Positioned(
                      bottom: 8,
                      right: 8,
                      child: Container(
                        padding:
                            const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                        decoration: BoxDecoration(
                          color: Colors.black.withValues(alpha: 0.75),
                          borderRadius: BorderRadius.circular(8),
                        ),
                        child: Text(
                          duration,
                          style: AppTextStyles.monoSmall.copyWith(
                            color: Colors.white.withValues(alpha: 0.85),
                          ),
                        ),
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 14),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(title, style: AppTextStyles.h2),
                    const SizedBox(height: 8),
                    Text(creator, style: AppTextStyles.bodySmall),
                    const SizedBox(height: 10),
                    const Row(
                      children: [
                        Icon(Icons.favorite_border, size: 16, color: AppColors.textMuted),
                        SizedBox(width: 4),
                        Text('17.4K', style: TextStyle(color: AppColors.textMuted)),
                      ],
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

class VideoCard extends StatelessWidget {
  const VideoCard({
    super.key,
    required this.title,
    required this.stats,
    this.onTap,
  });

  final String title;
  final String stats;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      label: 'Video: $title, $stats',
      child: GestureDetector(
        onTap: onTap,
        child: Container(
          margin: const EdgeInsets.only(bottom: 14),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                height: 180,
                decoration: const BoxDecoration(
                  borderRadius: BorderRadius.vertical(top: Radius.circular(AppSpacing.radiusXL)),
                  gradient: LinearGradient(
                    begin: Alignment.topLeft,
                    end: Alignment.bottomRight,
                    colors: [Color(0xFF1D3A46), Color(0xFF13131E)],
                  ),
                ),
                child: Stack(
                  children: [
                    Positioned(
                      top: 10,
                      left: 10,
                      child: Container(
                        padding:
                            const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                        decoration: BoxDecoration(
                          color: AppColors.posttubePrimary.withValues(alpha: 0.2),
                          borderRadius: BorderRadius.circular(999),
                        ),
                        child: Text(
                          'POSTTUBE',
                          style: AppTextStyles.labelTiny
                              .copyWith(color: AppColors.posttubePrimary),
                        ),
                      ),
                    ),
                    Center(
                      child: Semantics(
                        label: 'Play video',
                        button: true,
                        child: Container(
                          width: 50,
                          height: 50,
                          decoration: BoxDecoration(
                            color: Colors.white.withValues(alpha: 0.15),
                            shape: BoxShape.circle,
                            border: Border.all(color: AppColors.borderSubtle),
                          ),
                          child: const Icon(Icons.play_arrow, color: Colors.white),
                        ),
                      ),
                    ),
                  ],
                ),
              ),
              Padding(
                padding: const EdgeInsets.all(14),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(title, style: AppTextStyles.h2),
                    const SizedBox(height: 6),
                    Text(stats, style: AppTextStyles.bodySmall),
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
