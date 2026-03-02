import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/shared/widgets/content_cards.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';

class PosttubeScreen extends StatefulWidget {
  const PosttubeScreen({super.key});

  @override
  State<PosttubeScreen> createState() => _PosttubeScreenState();
}

class _PosttubeScreenState extends State<PosttubeScreen> {
  double _progress = 0.38;
  bool _playing = true;
  bool _subscribed = false;
  bool _descriptionExpanded = false;
  int _contentTab = 0;

  static const List<_Chapter> _chapters = [
    _Chapter(time: '00:00', label: 'Intro'),
    _Chapter(time: '02:14', label: 'Architecture'),
    _Chapter(time: '08:22', label: 'Feed Ranking'),
    _Chapter(time: '13:09', label: 'Realtime Events'),
    _Chapter(time: '19:41', label: 'Wrap Up'),
  ];

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: CustomScrollView(
          slivers: [
            SliverToBoxAdapter(
              child: Padding(
                padding: AppSpacing.pagePadding.copyWith(top: 10, bottom: 14),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    _VideoPanel(
                      progress: _progress,
                      isPlaying: _playing,
                      onProgressChanged: (value) => setState(() => _progress = value),
                      onTogglePlay: () => setState(() => _playing = !_playing),
                    ),
                    const SizedBox(height: 14),
                    Text(
                      'Building a scalable feed with event-driven architecture',
                      style: AppTextStyles.h2.copyWith(fontSize: 19),
                    ),
                    const SizedBox(height: 6),
                    Text(
                      '45.2K views  •  3h ago  •  4.9',
                      style: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
                    ),
                    const SizedBox(height: 14),
                    SingleChildScrollView(
                      scrollDirection: Axis.horizontal,
                      child: Row(
                        children: const [
                          ActionPillButton(icon: Icons.thumb_up_alt_outlined, label: '2.4K'),
                          SizedBox(width: 8),
                          ActionPillButton(icon: Icons.thumb_down_alt_outlined, label: '87'),
                          SizedBox(width: 8),
                          ActionPillButton(icon: Icons.share_outlined, label: 'Share'),
                          SizedBox(width: 8),
                          ActionPillButton(icon: Icons.download_outlined, label: 'Save'),
                          SizedBox(width: 8),
                          ActionPillButton(icon: Icons.playlist_add_outlined, label: 'Playlist'),
                        ],
                      ),
                    ),
                    const SizedBox(height: 16),
                    _ChannelCard(
                      subscribed: _subscribed,
                      onSubscribeTap: () => setState(() => _subscribed = !_subscribed),
                    ),
                    const SizedBox(height: 16),
                    Text('Chapters', style: AppTextStyles.h3),
                    const SizedBox(height: 10),
                    SizedBox(
                      height: 38,
                      child: ListView.separated(
                        scrollDirection: Axis.horizontal,
                        itemCount: _chapters.length,
                        separatorBuilder: (_, _) => const SizedBox(width: 8),
                        itemBuilder: (context, index) {
                          final chapter = _chapters[index];
                          return Container(
                            padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 8),
                            decoration: BoxDecoration(
                              color: AppColors.bgCard,
                              borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                              border: Border.all(color: AppColors.borderSubtle),
                            ),
                            child: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Text(
                                  chapter.time,
                                  style: AppTextStyles.monoSmall.copyWith(
                                    color: AppColors.posttubePrimary,
                                  ),
                                ),
                                const SizedBox(width: 6),
                                Text(chapter.label, style: AppTextStyles.labelSmall),
                              ],
                            ),
                          );
                        },
                      ),
                    ),
                    const SizedBox(height: 16),
                    _DescriptionCard(
                      expanded: _descriptionExpanded,
                      onTap: () => setState(() => _descriptionExpanded = !_descriptionExpanded),
                    ),
                    const SizedBox(height: 16),
                    _ContentTabs(
                      activeIndex: _contentTab,
                      onChanged: (value) => setState(() => _contentTab = value),
                    ),
                    const SizedBox(height: 12),
                    if (_contentTab == 0) const _CommentsSection() else const _UpNextSection(),
                    const SizedBox(height: 100),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _VideoPanel extends StatelessWidget {
  const _VideoPanel({
    required this.progress,
    required this.isPlaying,
    required this.onProgressChanged,
    required this.onTogglePlay,
  });

  final double progress;
  final bool isPlaying;
  final ValueChanged<double> onProgressChanged;
  final VoidCallback onTogglePlay;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgTertiary,
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          Container(
            height: 230,
            decoration: const BoxDecoration(
              borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
              gradient: LinearGradient(
                begin: Alignment.topLeft,
                end: Alignment.bottomRight,
                colors: [Color(0xFF163B42), Color(0xFF151524)],
              ),
            ),
            child: Stack(
              children: [
                Positioned(
                  top: 10,
                  left: 10,
                  right: 10,
                  child: Row(
                    children: [
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
                        decoration: BoxDecoration(
                          color: AppColors.posttubePrimary.withValues(alpha: 0.18),
                          borderRadius: BorderRadius.circular(999),
                        ),
                        child: Text(
                          'POSTTUBE',
                          style: AppTextStyles.labelTiny.copyWith(
                            color: AppColors.posttubePrimary,
                          ),
                        ),
                      ),
                      const Spacer(),
                      const GlassIconButton(icon: Icons.settings_outlined),
                    ],
                  ),
                ),
                Center(
                  child: GestureDetector(
                    onTap: onTogglePlay,
                    child: Container(
                      width: 62,
                      height: 62,
                      decoration: BoxDecoration(
                        color: Colors.white.withValues(alpha: 0.14),
                        shape: BoxShape.circle,
                        border: Border.all(color: Colors.white.withValues(alpha: 0.2)),
                      ),
                      child: Icon(
                        isPlaying ? Icons.pause : Icons.play_arrow,
                        color: Colors.white,
                        size: 28,
                      ),
                    ),
                  ),
                ),
              ],
            ),
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(10, 6, 10, 10),
            child: Column(
              children: [
                SliderTheme(
                  data: SliderTheme.of(context).copyWith(
                    thumbColor: AppColors.posttubePrimary,
                    activeTrackColor: AppColors.posttubePrimary,
                    inactiveTrackColor: Colors.white.withValues(alpha: 0.2),
                    overlayShape: SliderComponentShape.noOverlay,
                    thumbShape: const RoundSliderThumbShape(enabledThumbRadius: 6),
                  ),
                  child: Slider(
                    min: 0,
                    max: 1,
                    value: progress,
                    onChanged: onProgressChanged,
                  ),
                ),
                Row(
                  children: [
                    Text(
                      '08:22 / 21:47',
                      style: AppTextStyles.mono.copyWith(color: AppColors.textSecondary),
                    ),
                    const Spacer(),
                    IconButton(
                      onPressed: () {},
                      icon: const Icon(Icons.skip_previous, color: AppColors.textMuted),
                    ),
                    IconButton(
                      onPressed: onTogglePlay,
                      icon: Icon(
                        isPlaying ? Icons.pause_circle_outline : Icons.play_circle_outline,
                        color: AppColors.textSecondary,
                      ),
                    ),
                    IconButton(
                      onPressed: () {},
                      icon: const Icon(Icons.skip_next, color: AppColors.textMuted),
                    ),
                    IconButton(
                      onPressed: () {},
                      icon: const Icon(Icons.volume_up_outlined, color: AppColors.textMuted),
                    ),
                    IconButton(
                      onPressed: () {},
                      icon: const Icon(Icons.fullscreen, color: AppColors.textMuted),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _ChannelCard extends StatelessWidget {
  const _ChannelCard({
    required this.subscribed,
    required this.onSubscribeTap,
  });

  final bool subscribed;
  final VoidCallback onSubscribeTap;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Container(
            width: 46,
            height: 46,
            decoration: BoxDecoration(
              gradient: AppColors.posttubeGradient,
              shape: BoxShape.circle,
              border: Border.all(color: AppColors.posttubePrimary.withValues(alpha: 0.5)),
            ),
            child: Center(
              child: Text(
                'AT',
                style: AppTextStyles.label.copyWith(color: Colors.white),
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
                    Text('atpost engineering', style: AppTextStyles.h3),
                    const SizedBox(width: 4),
                    const Icon(
                      Icons.verified,
                      size: 16,
                      color: AppColors.posttubePrimary,
                    ),
                  ],
                ),
                const SizedBox(height: 2),
                Text('189K subscribers', style: AppTextStyles.bodySmall),
              ],
            ),
          ),
          GestureDetector(
            onTap: onSubscribeTap,
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
              decoration: BoxDecoration(
                gradient: subscribed ? null : AppColors.posttubeGradient,
                color: subscribed ? Colors.white.withValues(alpha: 0.06) : null,
                borderRadius: BorderRadius.circular(999),
                border: Border.all(
                  color: subscribed
                      ? AppColors.borderSubtle
                      : AppColors.posttubePrimary.withValues(alpha: 0.4),
                ),
              ),
              child: Text(
                subscribed ? 'Subscribed' : 'Subscribe',
                style: AppTextStyles.label.copyWith(
                  color: subscribed ? AppColors.textSecondary : Colors.white,
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _DescriptionCard extends StatelessWidget {
  const _DescriptionCard({
    required this.expanded,
    required this.onTap,
  });

  final bool expanded;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'Description',
              style: AppTextStyles.h3,
            ),
            const SizedBox(height: 6),
            Text(
              expanded
                  ? 'In this session we break down the gateway, fanout feed write pipeline, '
                      'ranking refresh strategy, and cache invalidation signals used to keep '
                      'the home timeline snappy under burst load.'
                  : 'In this session we break down the gateway, fanout feed write pipeline...',
              style: AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
            ),
            const SizedBox(height: 8),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                _TagChip(label: '#architecture'),
                _TagChip(label: '#golang'),
                _TagChip(label: '#distributed-systems'),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _TagChip extends StatelessWidget {
  const _TagChip({required this.label});

  final String label;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 5),
      decoration: BoxDecoration(
        color: AppColors.bgPrimary,
        borderRadius: BorderRadius.circular(999),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Text(label, style: AppTextStyles.tag),
    );
  }
}

class _ContentTabs extends StatelessWidget {
  const _ContentTabs({
    required this.activeIndex,
    required this.onChanged,
  });

  final int activeIndex;
  final ValueChanged<int> onChanged;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        _ContentTabItem(
          label: 'Comments (5)',
          active: activeIndex == 0,
          onTap: () => onChanged(0),
        ),
        const SizedBox(width: 8),
        _ContentTabItem(
          label: 'Up Next',
          active: activeIndex == 1,
          onTap: () => onChanged(1),
        ),
      ],
    );
  }
}

class _ContentTabItem extends StatelessWidget {
  const _ContentTabItem({
    required this.label,
    required this.active,
    required this.onTap,
  });

  final String label;
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 220),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
        decoration: BoxDecoration(
          gradient: active ? AppColors.posttubeGradient : null,
          color: active ? null : AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
          border: Border.all(
            color: active
                ? AppColors.posttubePrimary.withValues(alpha: 0.4)
                : AppColors.borderSubtle,
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.label.copyWith(
            color: active ? Colors.white : AppColors.textMuted,
          ),
        ),
      ),
    );
  }
}

class _CommentsSection extends StatelessWidget {
  const _CommentsSection();

  @override
  Widget build(BuildContext context) {
    return Column(
      children: const [
        _CommentTile(
          initials: 'NM',
          name: 'Neha Motion',
          time: '1h',
          text: 'Chapter markers are clean and the controls feel balanced.',
        ),
        SizedBox(height: 10),
        _CommentTile(
          initials: 'AD',
          name: 'Aarav Dev',
          time: '55m',
          text: 'Would love a follow-up on feed score explainability.',
        ),
        SizedBox(height: 10),
        _CommentTile(
          initials: 'TS',
          name: 'Tara Shah',
          time: '44m',
          text: 'Great pacing. Playlist action placement is solid.',
        ),
      ],
    );
  }
}

class _CommentTile extends StatelessWidget {
  const _CommentTile({
    required this.initials,
    required this.name,
    required this.time,
    required this.text,
  });

  final String initials;
  final String name;
  final String time;
  final String text;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(11),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 34,
            height: 34,
            decoration: BoxDecoration(
              color: AppColors.bgTertiary,
              borderRadius: BorderRadius.circular(12),
            ),
            child: Center(
              child: Text(initials, style: AppTextStyles.labelSmall),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(child: Text(name, style: AppTextStyles.h3)),
                    Text(time, style: AppTextStyles.monoSmall),
                  ],
                ),
                const SizedBox(height: 4),
                Text(text, style: AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary)),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _UpNextSection extends StatelessWidget {
  const _UpNextSection();

  @override
  Widget build(BuildContext context) {
    return Column(
      children: const [
        _RelatedVideoTile(
          title: 'Realtime notifications at scale',
          stats: '11K views • 5h ago',
        ),
        SizedBox(height: 10),
        _RelatedVideoTile(
          title: 'From monolith to service mesh',
          stats: '29K views • 1d ago',
        ),
        SizedBox(height: 10),
        _RelatedVideoTile(
          title: 'Optimizing write fanout and storage',
          stats: '8.2K views • 2d ago',
        ),
      ],
    );
  }
}

class _RelatedVideoTile extends StatelessWidget {
  const _RelatedVideoTile({
    required this.title,
    required this.stats,
  });

  final String title;
  final String stats;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Container(
            width: 120,
            height: 78,
            decoration: const BoxDecoration(
              borderRadius: BorderRadius.horizontal(left: Radius.circular(AppSpacing.radiusLarge)),
              gradient: LinearGradient(
                begin: Alignment.topLeft,
                end: Alignment.bottomRight,
                colors: [Color(0xFF1D4047), Color(0xFF1B1B28)],
              ),
            ),
            child: const Center(
              child: Icon(Icons.play_circle_fill, color: Colors.white70, size: 28),
            ),
          ),
          Expanded(
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(title, style: AppTextStyles.h3, maxLines: 2, overflow: TextOverflow.ellipsis),
                  const SizedBox(height: 5),
                  Text(stats, style: AppTextStyles.bodySmall),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _Chapter {
  const _Chapter({
    required this.time,
    required this.label,
  });

  final String time;
  final String label;
}
