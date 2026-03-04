import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/providers/feed_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ReelsScreen extends ConsumerStatefulWidget {
  const ReelsScreen({super.key, this.fullscreenRoute = false});

  final bool fullscreenRoute;

  @override
  ConsumerState<ReelsScreen> createState() => _ReelsScreenState();
}

class _ReelsScreenState extends ConsumerState<ReelsScreen> {
  late final PageController _pageController;
  int _currentIndex = 0;
  bool _muted = false;
  bool _paused = false;
  bool _showPlayIndicator = false;
  bool _showHeartBurst = false;
  final Set<int> _likedIndexes = <int>{};

  static const List<List<Color>> _gradientPalette = [
    [Color(0xFF2A1020), Color(0xFF0E0E18)],
    [Color(0xFF1C1031), Color(0xFF0A1222)],
    [Color(0xFF2A1F0F), Color(0xFF111220)],
    [Color(0xFF0F2A1E), Color(0xFF0A0E22)],
    [Color(0xFF2A2010), Color(0xFF1E110A)],
  ];

  static const List<String> _emojiPalette = ['🎬', '⚡', '✨', '🎵', '🔥'];

  static const List<_ReelData> _fallbackReels = [
    _ReelData(
      id: 'loading',
      user: '@atpost',
      title: 'Loading reels...',
      tags: '',
      song: '',
      emoji: '🎬',
      likes: '0',
      comments: '0',
      shares: '0',
      gradientColors: [Color(0xFF2A1020), Color(0xFF0E0E18)],
    ),
  ];

  List<_ReelData> _reels = _fallbackReels;

  List<_ReelData> _postsToReels(List<Post> posts) {
    return posts.asMap().entries.map((entry) {
      final i = entry.key;
      final post = entry.value;
      return _ReelData(
        id: post.id,
        user: '@${(post.authorName ?? 'user').toLowerCase().replaceAll(' ', '.')}',
        title: post.content.length > 80 ? '${post.content.substring(0, 80)}...' : post.content,
        tags: post.tags.join(' '),
        song: '',
        emoji: _emojiPalette[i % _emojiPalette.length],
        likes: _formatCount(post.likeCount),
        comments: _formatCount(post.commentCount),
        shares: _formatCount(post.shareCount),
        gradientColors: _gradientPalette[i % _gradientPalette.length],
      );
    }).toList();
  }

  String _formatCount(int count) {
    if (count >= 1000000) return '${(count / 1000000).toStringAsFixed(1)}M';
    if (count >= 1000) return '${(count / 1000).toStringAsFixed(1)}K';
    return count.toString();
  }

  @override
  void initState() {
    super.initState();
    _pageController = PageController();
  }

  @override
  void dispose() {
    _pageController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    ref.listen<AsyncValue<List<Post>>>(reelFeedProvider, (_, next) {
      next.whenData((posts) {
        if (mounted && posts.isNotEmpty) {
          setState(() {
            _reels = _postsToReels(posts);
            _currentIndex = 0;
          });
        }
      });
    });

    final safeIndex = _currentIndex.clamp(0, _reels.length - 1);
    final active = _reels[safeIndex];

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: GestureDetector(
        onTap: _togglePause,
        onDoubleTap: _doubleTapLike,
        child: Stack(
          children: [
            PageView.builder(
              controller: _pageController,
              scrollDirection: Axis.vertical,
              onPageChanged: (index) => setState(() {
                _currentIndex = index;
                _paused = false;
              }),
              itemCount: _reels.length,
              itemBuilder: (context, index) {
                final reel = _reels[index];
                return _ReelBackground(
                  reel: reel,
                  isActive: index == _currentIndex,
                  paused: _paused,
                );
              },
            ),
            Positioned.fill(
              child: IgnorePointer(
                child: DecoratedBox(
                  decoration: BoxDecoration(
                    gradient: LinearGradient(
                      begin: Alignment.topCenter,
                      end: Alignment.bottomCenter,
                      colors: [
                        Colors.black.withValues(alpha: 0.36),
                        Colors.transparent,
                        Colors.black.withValues(alpha: 0.5),
                      ],
                    ),
                  ),
                ),
              ),
            ),
            SafeArea(
              bottom: false,
              child: Padding(
                padding: const EdgeInsets.fromLTRB(12, 8, 12, 0),
                child: Column(
                  children: [
                    Row(
                      children: List.generate(_reels.length, (index) {
                        final isCompleted = index < _currentIndex;
                        final isActive = index == _currentIndex;
                        return Expanded(
                          child: Container(
                            margin: EdgeInsets.only(right: index == _reels.length - 1 ? 0 : 5),
                            height: 3,
                            decoration: BoxDecoration(
                              borderRadius: BorderRadius.circular(999),
                              color: isCompleted
                                  ? Colors.white
                                  : isActive
                                      ? AppColors.postgramPrimary
                                      : Colors.white.withValues(alpha: 0.24),
                            ),
                          ),
                        );
                      }),
                    ),
                    const SizedBox(height: 10),
                    Row(
                      children: [
                        if (widget.fullscreenRoute)
                          Padding(
                            padding: const EdgeInsets.only(right: 8),
                            child: _OverlayIconButton(
                              icon: Icons.arrow_back,
                              onTap: () => context.pop(),
                            ),
                          ),
                        Container(
                          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                          decoration: BoxDecoration(
                            color: AppColors.postgramPrimary.withValues(alpha: 0.2),
                            borderRadius: BorderRadius.circular(999),
                            border: Border.all(
                              color: AppColors.postgramPrimary.withValues(alpha: 0.3),
                            ),
                          ),
                          child: Row(
                            children: [
                              Text(
                                'POSTGRAM',
                                style: AppTextStyles.labelTiny.copyWith(
                                  color: AppColors.postgramPrimary,
                                ),
                              ),
                              const SizedBox(width: 6),
                              Container(
                                width: 6,
                                height: 6,
                                decoration: const BoxDecoration(
                                  color: AppColors.postgramPrimary,
                                  shape: BoxShape.circle,
                                ),
                              )
                                  .animate(onPlay: (controller) => controller.repeat())
                                  .fade(begin: 0.45, end: 1, duration: 900.ms),
                            ],
                          ),
                        ),
                        const Spacer(),
                        _OverlayIconButton(
                          icon: _muted ? Icons.volume_off_outlined : Icons.volume_up_outlined,
                          onTap: () => setState(() => _muted = !_muted),
                        ),
                        const SizedBox(width: 8),
                        const _OverlayIconButton(icon: Icons.more_horiz),
                      ],
                    ),
                  ],
                ),
              ),
            ),
            Positioned(
              right: 12,
              bottom: widget.fullscreenRoute ? 148 : 230,
              child: _ActionRail(
                liked: _likedIndexes.contains(_currentIndex),
                likes: active.likes,
                comments: active.comments,
                shares: active.shares,
                onLike: _toggleLike,
                onComment: _openComments,
                onShare: () {},
              ),
            ),
            Positioned(
              left: 12,
              right: 70,
              bottom: widget.fullscreenRoute ? 20 : 104,
              child: _BottomInfo(
                reel: active,
                onFollowTap: () {},
              ),
            ),
            Positioned(
              left: 0,
              right: 0,
              bottom: widget.fullscreenRoute ? 6 : 88,
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: List.generate(_reels.length, (index) {
                  final activeDot = index == _currentIndex;
                  return AnimatedContainer(
                    duration: const Duration(milliseconds: 200),
                    margin: const EdgeInsets.symmetric(horizontal: 3),
                    width: activeDot ? 18 : 6,
                    height: 6,
                    decoration: BoxDecoration(
                      color: activeDot
                          ? AppColors.postgramPrimary
                          : Colors.white.withValues(alpha: 0.35),
                      borderRadius: BorderRadius.circular(999),
                    ),
                  );
                }),
              ),
            ),
            if (_showPlayIndicator)
              Center(
                child: Container(
                  width: 76,
                  height: 76,
                  decoration: BoxDecoration(
                    color: Colors.black.withValues(alpha: 0.45),
                    shape: BoxShape.circle,
                    border: Border.all(color: Colors.white.withValues(alpha: 0.2)),
                  ),
                  child: Icon(
                    _paused ? Icons.play_arrow : Icons.pause,
                    size: 34,
                    color: Colors.white,
                  ),
                ),
              ),
            if (_showHeartBurst)
              Center(
                child: Text(
                  '❤',
                  style: AppTextStyles.h1.copyWith(
                    fontSize: 100,
                    color: AppColors.postgramPrimary,
                  ),
                )
                    .animate()
                    .scale(begin: const Offset(0.4, 0.4), end: const Offset(1.15, 1.15), duration: 180.ms)
                    .fadeOut(delay: 280.ms, duration: 220.ms),
              ),
          ],
        ),
      ),
    );
  }

  void _togglePause() {
    setState(() {
      _paused = !_paused;
      _showPlayIndicator = true;
    });
    Future<void>.delayed(const Duration(milliseconds: 620), () {
      if (!mounted) {
        return;
      }
      setState(() => _showPlayIndicator = false);
    });
  }

  void _toggleLike() {
    setState(() {
      if (_likedIndexes.contains(_currentIndex)) {
        _likedIndexes.remove(_currentIndex);
      } else {
        _likedIndexes.add(_currentIndex);
      }
    });
  }

  void _doubleTapLike() {
    setState(() {
      _likedIndexes.add(_currentIndex);
      _showHeartBurst = true;
    });
    Future<void>.delayed(const Duration(milliseconds: 520), () {
      if (!mounted) {
        return;
      }
      setState(() => _showHeartBurst = false);
    });
  }

  Future<void> _openComments() async {
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (context) => const _CommentsSheet(),
    );
  }
}

class _ReelBackground extends StatelessWidget {
  const _ReelBackground({
    required this.reel,
    required this.isActive,
    required this.paused,
  });

  final _ReelData reel;
  final bool isActive;
  final bool paused;

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topCenter,
          end: Alignment.bottomCenter,
          colors: reel.gradientColors,
        ),
      ),
      child: Stack(
        children: [
          const _ParticleLayer(),
          Center(
            child: Text(
              reel.emoji,
              style: AppTextStyles.h1.copyWith(fontSize: 120),
            )
                .animate(
                  target: isActive && !paused ? 1 : 0,
                  onPlay: (controller) => controller.repeat(reverse: true),
                )
                .moveY(begin: 0, end: -10, duration: 1600.ms, curve: Curves.easeInOut)
                .scale(begin: const Offset(1, 1), end: const Offset(1.03, 1.03), duration: 1600.ms),
          ),
        ],
      ),
    );
  }
}

class _ParticleLayer extends StatelessWidget {
  const _ParticleLayer();

  @override
  Widget build(BuildContext context) {
    return Stack(
      children: [
        _Particle(left: 26, top: 180, size: 72, color: AppColors.postgramPrimary.withValues(alpha: 0.18)),
        _Particle(left: 220, top: 270, size: 58, color: AppColors.accentPurple.withValues(alpha: 0.2)),
        _Particle(left: 140, top: 480, size: 62, color: AppColors.postbookPrimary.withValues(alpha: 0.14)),
        _Particle(left: 290, top: 620, size: 54, color: AppColors.postgramSecondary.withValues(alpha: 0.16)),
        _Particle(left: 54, top: 700, size: 48, color: AppColors.posttubePrimary.withValues(alpha: 0.14)),
      ],
    );
  }
}

class _Particle extends StatelessWidget {
  const _Particle({
    required this.left,
    required this.top,
    required this.size,
    required this.color,
  });

  final double left;
  final double top;
  final double size;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Positioned(
      left: left,
      top: top,
      child: Container(
        width: size,
        height: size,
        decoration: BoxDecoration(
          color: color,
          shape: BoxShape.circle,
        ),
      )
          .animate(onPlay: (controller) => controller.repeat(reverse: true))
          .moveY(begin: -8, end: 8, duration: 2600.ms, curve: Curves.easeInOut)
          .scale(begin: const Offset(0.92, 0.92), end: const Offset(1.05, 1.05), duration: 2600.ms),
    );
  }
}

class _OverlayIconButton extends StatelessWidget {
  const _OverlayIconButton({
    required this.icon,
    this.onTap,
  });

  final IconData icon;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: 38,
        height: 38,
        decoration: BoxDecoration(
          color: Colors.white.withValues(alpha: 0.1),
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: Colors.white.withValues(alpha: 0.1)),
        ),
        child: Icon(icon, color: Colors.white, size: 19),
      ),
    );
  }
}

class _ActionRail extends StatelessWidget {
  const _ActionRail({
    required this.liked,
    required this.likes,
    required this.comments,
    required this.shares,
    required this.onLike,
    required this.onComment,
    required this.onShare,
  });

  final bool liked;
  final String likes;
  final String comments;
  final String shares;
  final VoidCallback onLike;
  final VoidCallback onComment;
  final VoidCallback onShare;

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        _RailItem(
          icon: liked ? Icons.favorite : Icons.favorite_border,
          label: likes,
          iconColor: liked ? AppColors.postgramPrimary : Colors.white,
          glow: liked,
          onTap: onLike,
        ),
        const SizedBox(height: 14),
        _RailItem(
          icon: Icons.chat_bubble_outline,
          label: comments,
          onTap: onComment,
        ),
        const SizedBox(height: 14),
        _RailItem(
          icon: Icons.share_outlined,
          label: shares,
          onTap: onShare,
        ),
        const SizedBox(height: 14),
        const _RailItem(
          icon: Icons.bookmark_border,
          label: 'Save',
        ),
        const SizedBox(height: 16),
        Container(
          width: 42,
          height: 42,
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: 0.12),
            shape: BoxShape.circle,
            border: Border.all(color: Colors.white.withValues(alpha: 0.18)),
          ),
          child: const Icon(Icons.music_note, color: Colors.white),
        )
            .animate(onPlay: (controller) => controller.repeat())
            .rotate(duration: 3000.ms),
      ],
    );
  }
}

class _RailItem extends StatelessWidget {
  const _RailItem({
    required this.icon,
    required this.label,
    this.iconColor = Colors.white,
    this.glow = false,
    this.onTap,
  });

  final IconData icon;
  final String label;
  final Color iconColor;
  final bool glow;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Column(
        children: [
          Container(
            width: 44,
            height: 44,
            decoration: BoxDecoration(
              color: Colors.white.withValues(alpha: 0.12),
              shape: BoxShape.circle,
              border: Border.all(color: Colors.white.withValues(alpha: 0.16)),
              boxShadow: glow
                  ? const [
                      BoxShadow(
                        color: Color(0x66FF3366),
                        blurRadius: 16,
                        spreadRadius: 1,
                      ),
                    ]
                  : const [],
            ),
            child: Icon(icon, color: iconColor, size: 22),
          ),
          const SizedBox(height: 5),
          Text(
            label,
            style: AppTextStyles.labelSmall.copyWith(color: Colors.white),
          ),
        ],
      ),
    );
  }
}

class _BottomInfo extends StatelessWidget {
  const _BottomInfo({
    required this.reel,
    required this.onFollowTap,
  });

  final _ReelData reel;
  final VoidCallback onFollowTap;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Row(
          children: [
            Container(
              width: 38,
              height: 38,
              decoration: BoxDecoration(
                gradient: AppColors.postbookGradient,
                borderRadius: BorderRadius.circular(12),
              ),
              child: Center(
                child: Text(
                  reel.user.substring(1, 2).toUpperCase(),
                  style: AppTextStyles.h3.copyWith(color: Colors.white),
                ),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                reel.user,
                style: AppTextStyles.h3.copyWith(color: Colors.white),
              ),
            ),
            GestureDetector(
              onTap: onFollowTap,
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
                decoration: BoxDecoration(
                  color: AppColors.postgramPrimary,
                  borderRadius: BorderRadius.circular(999),
                ),
                child: Text(
                  'Follow',
                  style: AppTextStyles.label.copyWith(color: Colors.white),
                ),
              ),
            ),
          ],
        ),
        const SizedBox(height: 8),
        Text(
          reel.title,
          style: AppTextStyles.body.copyWith(color: Colors.white),
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
        ),
        const SizedBox(height: 5),
        Text(
          reel.tags,
          style: AppTextStyles.monoSmall.copyWith(color: Colors.white.withValues(alpha: 0.68)),
        ),
        const SizedBox(height: 8),
        ClipRect(
          child: SizedBox(
            height: 16,
            child: SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              physics: const NeverScrollableScrollPhysics(),
              child: Text(
                'Music  ${reel.song}  •  Music  ${reel.song}  •',
                style: AppTextStyles.labelSmall.copyWith(
                  color: Colors.white.withValues(alpha: 0.82),
                ),
              )
                  .animate(onPlay: (controller) => controller.repeat())
                  .moveX(begin: 0, end: -120, duration: 7000.ms, curve: Curves.linear),
            ),
          ),
        ),
      ],
    );
  }
}

class _CommentsSheet extends StatefulWidget {
  const _CommentsSheet();

  @override
  State<_CommentsSheet> createState() => _CommentsSheetState();
}

class _CommentsSheetState extends State<_CommentsSheet> {
  final TextEditingController _commentController = TextEditingController();

  bool get _hasText => _commentController.text.trim().isNotEmpty;

  static const List<_Comment> _comments = [
    _Comment(
      user: 'aarav',
      text: 'The transition timing is so smooth.',
      time: '1h',
      likes: 118,
    ),
    _Comment(
      user: 'meera',
      text: 'Can you share the typography scale too?',
      time: '52m',
      likes: 72,
    ),
    _Comment(
      user: 'tara',
      text: 'Gradient balance is exactly right.',
      time: '37m',
      likes: 44,
    ),
  ];

  @override
  void initState() {
    super.initState();
    _commentController.addListener(_update);
  }

  @override
  void dispose() {
    _commentController
      ..removeListener(_update)
      ..dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return FractionallySizedBox(
      heightFactor: 0.6,
      child: Container(
        decoration: const BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
        ),
        child: Column(
          children: [
            const SizedBox(height: 10),
            Container(
              width: 40,
              height: 4,
              decoration: BoxDecoration(
                color: Colors.white.withValues(alpha: 0.2),
                borderRadius: BorderRadius.circular(999),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
              child: Row(
                children: [
                  Text('Comments', style: AppTextStyles.h2),
                  const Spacer(),
                  IconButton(
                    onPressed: () => context.pop(),
                    icon: const Icon(Icons.close, color: AppColors.textSecondary),
                  ),
                ],
              ),
            ),
            Expanded(
              child: ListView.separated(
                padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                itemCount: _comments.length,
                separatorBuilder: (_, _) => const SizedBox(height: 12),
                itemBuilder: (context, index) {
                  final comment = _comments[index];
                  return Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Container(
                        width: 34,
                        height: 34,
                        decoration: BoxDecoration(
                          color: AppColors.bgTertiary,
                          borderRadius: BorderRadius.circular(11),
                        ),
                        child: Center(
                          child: Text(
                            comment.user.substring(0, 1).toUpperCase(),
                            style: AppTextStyles.label,
                          ),
                        ),
                      ),
                      const SizedBox(width: 10),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Row(
                              children: [
                                Text(comment.user, style: AppTextStyles.h3),
                                const SizedBox(width: 8),
                                Text(
                                  comment.time,
                                  style: AppTextStyles.monoSmall.copyWith(color: AppColors.textDim),
                                ),
                              ],
                            ),
                            const SizedBox(height: 3),
                            Text(
                              comment.text,
                              style: AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
                            ),
                            const SizedBox(height: 4),
                            Text(
                              '❤ ${comment.likes}   Reply',
                              style: AppTextStyles.labelSmall.copyWith(
                                color: AppColors.textMuted,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ],
                  );
                },
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 8, 16, 14),
              child: Row(
                children: [
                  Expanded(
                    child: Container(
                      decoration: BoxDecoration(
                        color: AppColors.bgCard,
                        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                        border: Border.all(color: AppColors.borderSubtle),
                      ),
                      child: TextField(
                        controller: _commentController,
                        style: AppTextStyles.bodySmall,
                        decoration: InputDecoration(
                          border: InputBorder.none,
                          hintText: 'Add a comment',
                          hintStyle: AppTextStyles.bodySmall.copyWith(color: AppColors.textGhost),
                          contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Container(
                    width: 40,
                    height: 40,
                    decoration: BoxDecoration(
                      gradient: _hasText ? AppColors.ctaGradient : null,
                      color: _hasText ? null : AppColors.bgCard,
                      shape: BoxShape.circle,
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: Icon(
                      Icons.send,
                      size: 18,
                      color: _hasText ? Colors.white : AppColors.textMuted,
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

  void _update() {
    if (!mounted) {
      return;
    }
    setState(() {});
  }
}

class _ReelData {
  const _ReelData({
    required this.id,
    required this.user,
    required this.title,
    required this.tags,
    required this.song,
    required this.emoji,
    required this.likes,
    required this.comments,
    required this.shares,
    required this.gradientColors,
  });

  final String id;
  final String user;
  final String title;
  final String tags;
  final String song;
  final String emoji;
  final String likes;
  final String comments;
  final String shares;
  final List<Color> gradientColors;
}

class _Comment {
  const _Comment({
    required this.user,
    required this.text,
    required this.time,
    required this.likes,
  });

  final String user;
  final String text;
  final String time;
  final int likes;
}
