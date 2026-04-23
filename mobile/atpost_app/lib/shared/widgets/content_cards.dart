import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_animate/flutter_animate.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PostCard extends ConsumerStatefulWidget {
  const PostCard({super.key, required this.post});

  final Post post;

  @override
  ConsumerState<PostCard> createState() => _PostCardState();
}

class _PostCardState extends ConsumerState<PostCard> {
  late bool _liked;
  late int _likeCount;
  late int _shareCount;
  bool _actionPending = false;

  Post get post => widget.post;

  @override
  void initState() {
    super.initState();
    _liked = post.isLiked;
    _likeCount = post.likeCount;
    _shareCount = post.shareCount;
  }

  @override
  void didUpdateWidget(PostCard oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.post.id != widget.post.id) {
      _liked = widget.post.isLiked;
      _likeCount = widget.post.likeCount;
      _shareCount = widget.post.shareCount;
      _actionPending = false;
    }
  }

  String _formatCount(int count) {
    if (count >= 1000000) return '${(count / 1000000).toStringAsFixed(1)}M';
    if (count >= 1000) return '${(count / 1000).toStringAsFixed(1)}K';
    return count.toString();
  }

  String get _postLink {
    final base = Environment.externalDomain == null
        ? Environment.apiBaseUrl
        : 'https://${Environment.externalDomain}';
    return '$base/posts/${post.id}';
  }

  Future<void> _toggleReaction() async {
    if (_actionPending) return;

    final previousLiked = _liked;
    final previousCount = _likeCount;
    setState(() {
      _actionPending = true;
      _liked = !_liked;
      _likeCount += _liked ? 1 : -1;
      if (_likeCount < 0) _likeCount = 0;
    });

    try {
      await ref.read(postRepositoryProvider).toggleReaction(post.id);
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _liked = previousLiked;
        _likeCount = previousCount;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update reaction.')),
      );
    } finally {
      if (mounted) setState(() => _actionPending = false);
    }
  }

  Future<void> _sharePost() async {
    await Clipboard.setData(ClipboardData(text: _postLink));
    if (!mounted) return;

    setState(() => _shareCount += 1);
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Post link copied.')),
    );

    try {
      await ref.read(postRepositoryProvider).sharePost(post.id);
    } catch (_) {
      if (mounted && _shareCount > 0) setState(() => _shareCount -= 1);
    }
  }

  Future<void> _reportPost() async {
    final reason = await showModalBottomSheet<String>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: const [
            _ReportReasonTile(label: 'Spam', value: 'spam'),
            _ReportReasonTile(label: 'Harassment', value: 'harassment'),
            _ReportReasonTile(label: 'Hate speech', value: 'hate_speech'),
            _ReportReasonTile(label: 'Violence', value: 'violence'),
            _ReportReasonTile(label: 'Nudity', value: 'nudity'),
            _ReportReasonTile(label: 'Misinformation', value: 'misinformation'),
            _ReportReasonTile(label: 'Other', value: 'other'),
          ],
        ),
      ),
    );
    if (reason == null) return;

    try {
      await ref.read(postRepositoryProvider).submitReport(
            targetType: post.isReel ? 'reel' : (post.isVideo ? 'video' : 'post'),
            targetId: post.id,
            reason: reason,
          );
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Report submitted.')),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not submit report.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    // PRODUCTION OPTIMIZATION: RepaintBoundary isolates this complex card from other screen repaints.
    return RepaintBoundary(
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
            _buildHeader(context),
            const SizedBox(height: 12),
            Text(post.content, style: AppTextStyles.body),
            if (post.tags.isNotEmpty) _buildTags(),
            const SizedBox(height: 12),
            _buildActionRow(),
          ],
        ),
      ),
    ).animate().fadeIn(duration: 400.ms).slideY(begin: 0.05, end: 0);
  }

  Widget _buildHeader(BuildContext context) {
    return Row(
      children: [
        CircleAvatar(
          radius: 20,
          backgroundColor: AppColors.bgTertiary,
          backgroundImage: post.authorAvatar != null
              ? NetworkImage(post.authorAvatar!)
              : null,
          child: post.authorAvatar == null
              ? Text(post.authorName?[0] ?? '?', style: AppTextStyles.h3)
              : null,
        ),
        const SizedBox(width: 10),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(post.authorName ?? 'Anonymous', style: AppTextStyles.h3),
              Text(
                '@${(post.authorName ?? 'user').toLowerCase().replaceAll(' ', '_')} \u2022 ${_timeAgo(post.createdAt)}',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textDim,
                ),
              ),
            ],
          ),
        ),
        _buildMoreMenu(context),
      ],
    );
  }

  Widget _buildMoreMenu(BuildContext context) {
    return PopupMenuButton<String>(
      color: AppColors.bgSecondary,
      icon: const Icon(Icons.more_horiz, color: AppColors.textMuted),
      onSelected: (value) {
        switch (value) {
          case 'copy':
            Clipboard.setData(ClipboardData(text: _postLink));
            ScaffoldMessenger.of(context).showSnackBar(
              const SnackBar(content: Text('Post link copied.')),
            );
            return;
          case 'report':
            _reportPost();
            return;
        }
      },
      itemBuilder: (context) => [
        const PopupMenuItem(value: 'copy', child: Text('Copy link')),
        const PopupMenuItem(value: 'report', child: Text('Report')),
      ],
    );
  }

  Widget _buildTags() {
    return Padding(
      padding: const EdgeInsets.only(top: 10),
      child: Wrap(
        spacing: 8,
        runSpacing: 6,
        children: post.tags
            .map(
              (tag) => Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 5),
                decoration: BoxDecoration(
                  color: Colors.white.withOpacity(0.05),
                  borderRadius: BorderRadius.circular(999),
                ),
                child: Text(
                  '#$tag',
                  style: AppTextStyles.tag.copyWith(
                    color: AppColors.postbookPrimary,
                  ),
                ),
              ),
            )
            .toList(),
      ),
    );
  }

  Widget _buildActionRow() {
    return Row(
      children: [
        _ActionPill(
          icon: _liked ? Icons.favorite : Icons.favorite_border,
          label: _formatCount(_likeCount),
          active: _liked,
          onTap: _toggleReaction,
        ),
        const SizedBox(width: 8),
        _ActionPill(
          icon: Icons.chat_bubble_outline,
          label: _formatCount(post.commentCount),
          onTap: () => context.push('/comments/${post.id}'),
        ),
        const Spacer(),
        InkWell(
          onTap: _sharePost,
          borderRadius: BorderRadius.circular(999),
          child: Padding(
            padding: const EdgeInsets.all(8),
            child: Row(
              children: [
                const Icon(
                  Icons.share_outlined,
                  size: 20,
                  color: AppColors.textMuted,
                ),
                if (_shareCount > 0) ...[
                  const SizedBox(width: 4),
                  Text(
                    _formatCount(_shareCount),
                    style: AppTextStyles.labelSmall,
                  ),
                ],
              ],
            ),
          ),
        ),
      ],
    );
  }

  String _timeAgo(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inDays > 0) return '${diff.inDays}d';
    if (diff.inHours > 0) return '${diff.inHours}h';
    return '${diff.inMinutes}m';
  }
}

class _ReportReasonTile extends StatelessWidget {
  const _ReportReasonTile({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      title: Text(label, style: AppTextStyles.body),
      onTap: () => Navigator.of(context).pop(value),
    );
  }
}

class ReelCard extends StatelessWidget {
  final String title;
  final String creator;
  final String duration;
  final VoidCallback onTap;

  const ReelCard({
    super.key,
    required this.title,
    required this.creator,
    required this.duration,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        height: 200,
        margin: const EdgeInsets.only(bottom: 14),
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
          image: const DecorationImage(
            image: NetworkImage(
              'https://images.unsplash.com/photo-1611162617474-5b21e879e113?w=800&q=80',
            ),
            fit: BoxFit.cover,
          ),
        ),
        child: Stack(
          children: [
            Container(
              decoration: BoxDecoration(
                borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                gradient: LinearGradient(
                  begin: Alignment.topCenter,
                  end: Alignment.bottomCenter,
                  colors: [Colors.transparent, Colors.black.withOpacity(0.8)],
                ),
              ),
            ),
            Positioned(
              left: 16,
              bottom: 16,
              right: 16,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    style: AppTextStyles.h3.copyWith(color: Colors.white),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 4),
                  Row(
                    children: [
                      Text(
                        creator,
                        style: AppTextStyles.bodySmall.copyWith(
                          color: Colors.white70,
                        ),
                      ),
                      const Spacer(),
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 6,
                          vertical: 2,
                        ),
                        decoration: BoxDecoration(
                          color: Colors.black54,
                          borderRadius: BorderRadius.circular(4),
                        ),
                        child: Text(
                          duration,
                          style: AppTextStyles.labelTiny.copyWith(
                            color: Colors.white,
                          ),
                        ),
                      ),
                    ],
                  ),
                ],
              ),
            ),
            const Center(
              child: Icon(
                Icons.play_circle_outline,
                color: Colors.white,
                size: 50,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class VideoCard extends StatelessWidget {
  final String title;
  final String stats;
  final VoidCallback onTap;

  const VideoCard({
    super.key,
    required this.title,
    required this.stats,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        margin: const EdgeInsets.only(bottom: 14),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            AspectRatio(
              aspectRatio: 16 / 9,
              child: Container(
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                  image: const DecorationImage(
                    image: NetworkImage(
                      'https://images.unsplash.com/photo-1492691527719-9d1e07e534b4?w=800&q=80',
                    ),
                    fit: BoxFit.cover,
                  ),
                ),
                child: const Center(
                  child: Icon(
                    Icons.play_arrow_rounded,
                    color: Colors.white,
                    size: 40,
                  ),
                ),
              ),
            ),
            const SizedBox(height: 10),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 4),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    style: AppTextStyles.h3,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 4),
                  Text(
                    stats,
                    style: AppTextStyles.bodySmall.copyWith(
                      color: AppColors.textDim,
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ActionPill extends StatelessWidget {
  final IconData icon;
  final String label;
  final bool active;
  final VoidCallback onTap;

  const _ActionPill({
    required this.icon,
    required this.label,
    required this.onTap,
    this.active = false,
  });

  @override
  Widget build(BuildContext context) {
    final color = active ? AppColors.postbookPrimary : AppColors.textSecondary;
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          color: active
              ? color.withOpacity(0.1)
              : Colors.white.withOpacity(0.05),
          borderRadius: BorderRadius.circular(20),
        ),
        child: Row(
          children: [
            Icon(icon, size: 16, color: color),
            const SizedBox(width: 6),
            Text(
              label,
              style: AppTextStyles.label.copyWith(
                color: color,
                fontWeight: FontWeight.bold,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
