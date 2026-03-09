import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/live_stream.dart';
import 'package:atpost_app/data/repositories/live_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class LiveScreen extends ConsumerStatefulWidget {
  const LiveScreen({super.key});

  @override
  ConsumerState<LiveScreen> createState() => _LiveScreenState();
}

class _LiveScreenState extends ConsumerState<LiveScreen> {
  bool _loading = true;
  String? _error;
  List<LiveStream> _streams = const [];
  final Set<String> _joiningStreamIds = <String>{};
  final Set<String> _likingStreamIds = <String>{};

  @override
  void initState() {
    super.initState();
    _loadStreams();
  }

  Future<void> _loadStreams({bool showLoader = true}) async {
    if (showLoader) {
      setState(() {
        _loading = true;
        _error = null;
      });
    }

    try {
      final streams = await ref
          .read(liveRepositoryProvider)
          .getLiveStreams(limit: 40);
      streams.sort((a, b) => b.totalViewers.compareTo(a.totalViewers));

      if (!mounted) return;
      setState(() {
        _streams = streams;
        _loading = false;
        _error = null;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = 'Could not load live streams.';
      });
    }
  }

  Future<void> _joinStream(LiveStream stream) async {
    if (_joiningStreamIds.contains(stream.id)) return;

    setState(() => _joiningStreamIds.add(stream.id));
    try {
      final viewerCount = await ref
          .read(liveRepositoryProvider)
          .joinStream(stream.id);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Joined stream. Viewers now: $viewerCount')),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Could not join stream.')));
    } finally {
      if (mounted) {
        setState(() => _joiningStreamIds.remove(stream.id));
      }
    }
  }

  Future<void> _likeStream(LiveStream stream) async {
    if (_likingStreamIds.contains(stream.id)) return;

    setState(() => _likingStreamIds.add(stream.id));
    try {
      await ref.read(liveRepositoryProvider).likeStream(stream.id);
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('You liked this stream.')));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Could not like stream.')));
    } finally {
      if (mounted) {
        setState(() => _likingStreamIds.remove(stream.id));
      }
    }
  }

  Future<void> _openGoLiveSheet() async {
    final titleController = TextEditingController();
    final descriptionController = TextEditingController();

    final stream = await showModalBottomSheet<LiveStream>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (context) {
        bool submitting = false;
        String visibility = 'public';

        return StatefulBuilder(
          builder: (context, setModalState) {
            return SafeArea(
              top: false,
              child: Padding(
                padding: EdgeInsets.only(
                  left: 18,
                  right: 18,
                  top: 16,
                  bottom: MediaQuery.of(context).viewInsets.bottom + 16,
                ),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('Start a Live Stream', style: AppTextStyles.h2),
                    const SizedBox(height: 12),
                    TextField(
                      controller: titleController,
                      maxLength: 80,
                      decoration: InputDecoration(
                        hintText: 'Title your stream',
                        hintStyle: AppTextStyles.bodySmall,
                      ),
                    ),
                    const SizedBox(height: 10),
                    TextField(
                      controller: descriptionController,
                      maxLength: 140,
                      minLines: 2,
                      maxLines: 3,
                      decoration: InputDecoration(
                        hintText: 'Short description (optional)',
                        hintStyle: AppTextStyles.bodySmall,
                      ),
                    ),
                    const SizedBox(height: 10),
                    Text('Visibility', style: AppTextStyles.label),
                    const SizedBox(height: 8),
                    Wrap(
                      spacing: 8,
                      children: ['public', 'followers'].map((value) {
                        final selected = visibility == value;
                        return ChoiceChip(
                          label: Text(value),
                          selected: selected,
                          onSelected: (_) =>
                              setModalState(() => visibility = value),
                          selectedColor: AppColors.postbookPrimary.withValues(
                            alpha: 0.2,
                          ),
                          backgroundColor: AppColors.bgCard,
                          side: BorderSide(
                            color: selected
                                ? AppColors.postbookPrimary
                                : AppColors.borderSubtle,
                          ),
                          labelStyle: AppTextStyles.label.copyWith(
                            color: selected
                                ? AppColors.postbookPrimary
                                : AppColors.textSecondary,
                          ),
                        );
                      }).toList(),
                    ),
                    const SizedBox(height: 16),
                    SizedBox(
                      width: double.infinity,
                      child: ElevatedButton.icon(
                        onPressed: submitting
                            ? null
                            : () async {
                                final title = titleController.text.trim();
                                if (title.isEmpty) {
                                  ScaffoldMessenger.of(context).showSnackBar(
                                    const SnackBar(
                                      content: Text(
                                        'Please add a stream title.',
                                      ),
                                    ),
                                  );
                                  return;
                                }

                                setModalState(() => submitting = true);
                                try {
                                  final repo = ref.read(liveRepositoryProvider);
                                  final created = await repo.createStream(
                                    title: title,
                                    description: descriptionController.text
                                        .trim(),
                                    visibility: visibility,
                                  );
                                  await repo.goLive(created.id);
                                  if (!context.mounted) return;
                                  Navigator.of(context).pop(created);
                                } catch (_) {
                                  if (!context.mounted) return;
                                  ScaffoldMessenger.of(context).showSnackBar(
                                    const SnackBar(
                                      content: Text(
                                        'Could not start live stream.',
                                      ),
                                    ),
                                  );
                                } finally {
                                  if (context.mounted) {
                                    setModalState(() => submitting = false);
                                  }
                                }
                              },
                        style: ElevatedButton.styleFrom(
                          backgroundColor: AppColors.liveRed,
                          foregroundColor: Colors.white,
                          shape: RoundedRectangleBorder(
                            borderRadius: BorderRadius.circular(12),
                          ),
                          padding: const EdgeInsets.symmetric(vertical: 14),
                        ),
                        icon: submitting
                            ? const SizedBox(
                                width: 14,
                                height: 14,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                  color: Colors.white,
                                ),
                              )
                            : const Icon(Icons.videocam_rounded),
                        label: const Text('Go Live'),
                      ),
                    ),
                  ],
                ),
              ),
            );
          },
        );
      },
    );

    titleController.dispose();
    descriptionController.dispose();

    if (stream == null || !mounted) return;

    setState(() {
      _streams = [stream, ..._streams];
    });

    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('Live stream "${stream.title}" started.')),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: RefreshIndicator(
          color: AppColors.liveRed,
          onRefresh: () => _loadStreams(showLoader: false),
          child: CustomScrollView(
            physics: const AlwaysScrollableScrollPhysics(),
            slivers: [
              SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 8),
                  child: _LiveHeader(onGoLiveTap: _openGoLiveSheet),
                ),
              ),
              if (_loading)
                const SliverFillRemaining(
                  hasScrollBody: false,
                  child: Center(
                    child: CircularProgressIndicator(color: AppColors.liveRed),
                  ),
                )
              else if (_error != null)
                SliverFillRemaining(
                  hasScrollBody: false,
                  child: Center(
                    child: Padding(
                      padding: AppSpacing.pagePadding,
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(
                            Icons.live_tv_outlined,
                            size: 42,
                            color: AppColors.textMuted,
                          ),
                          const SizedBox(height: 10),
                          Text(_error!, style: AppTextStyles.bodySmall),
                          const SizedBox(height: 8),
                          TextButton(
                            onPressed: _loadStreams,
                            child: Text(
                              'Retry',
                              style: AppTextStyles.label.copyWith(
                                color: AppColors.liveRed,
                              ),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                )
              else if (_streams.isEmpty)
                SliverFillRemaining(
                  hasScrollBody: false,
                  child: Center(
                    child: Padding(
                      padding: AppSpacing.pagePadding,
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(
                            Icons.podcasts_rounded,
                            size: 48,
                            color: AppColors.textMuted,
                          ),
                          const SizedBox(height: 12),
                          Text(
                            'No live streams right now',
                            style: AppTextStyles.h3,
                          ),
                          const SizedBox(height: 6),
                          Text(
                            'Be the first one to go live in your community.',
                            textAlign: TextAlign.center,
                            style: AppTextStyles.bodySmall,
                          ),
                          const SizedBox(height: 14),
                          ElevatedButton.icon(
                            onPressed: _openGoLiveSheet,
                            style: ElevatedButton.styleFrom(
                              backgroundColor: AppColors.liveRed,
                              foregroundColor: Colors.white,
                              shape: RoundedRectangleBorder(
                                borderRadius: BorderRadius.circular(12),
                              ),
                            ),
                            icon: const Icon(Icons.videocam_rounded),
                            label: const Text('Start stream'),
                          ),
                        ],
                      ),
                    ),
                  ),
                )
              else
                SliverPadding(
                  padding: AppSpacing.pagePadding.copyWith(bottom: 110),
                  sliver: SliverList.separated(
                    itemCount: _streams.length,
                    separatorBuilder: (_, _) => const SizedBox(height: 12),
                    itemBuilder: (context, index) {
                      final stream = _streams[index];
                      return _LiveStreamCard(
                        stream: stream,
                        joining: _joiningStreamIds.contains(stream.id),
                        liking: _likingStreamIds.contains(stream.id),
                        onJoin: () => _joinStream(stream),
                        onLike: () => _likeStream(stream),
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
}

class _LiveHeader extends StatelessWidget {
  const _LiveHeader({required this.onGoLiveTap});

  final VoidCallback onGoLiveTap;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        gradient: const LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [Color(0x33FF3366), Color(0x334ECDC4)],
        ),
        border: Border.all(color: AppColors.borderMedium),
      ),
      child: Row(
        children: [
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
            decoration: BoxDecoration(
              color: AppColors.liveRed,
              borderRadius: BorderRadius.circular(999),
            ),
            child: Text(
              'LIVE',
              style: AppTextStyles.labelSmall.copyWith(color: Colors.white),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Live Streams', style: AppTextStyles.h1),
                Text(
                  'Watch creators in real-time or start your own stream.',
                  style: AppTextStyles.bodySmall,
                ),
              ],
            ),
          ),
          ElevatedButton.icon(
            onPressed: onGoLiveTap,
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.liveRed,
              foregroundColor: Colors.white,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(12),
              ),
            ),
            icon: const Icon(Icons.videocam_rounded, size: 16),
            label: const Text('Go Live'),
          ),
        ],
      ),
    );
  }
}

class _LiveStreamCard extends StatelessWidget {
  const _LiveStreamCard({
    required this.stream,
    required this.joining,
    required this.liking,
    required this.onJoin,
    required this.onLike,
  });

  final LiveStream stream;
  final bool joining;
  final bool liking;
  final VoidCallback onJoin;
  final VoidCallback onLike;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          AspectRatio(
            aspectRatio: 16 / 9,
            child: Container(
              decoration: BoxDecoration(
                borderRadius: const BorderRadius.vertical(
                  top: Radius.circular(16),
                ),
                gradient: const LinearGradient(
                  colors: [
                    Color(0x33111111),
                    Color(0x33FF3366),
                    Color(0x334ECDC4),
                  ],
                  begin: Alignment.topLeft,
                  end: Alignment.bottomRight,
                ),
              ),
              child: Stack(
                children: [
                  if ((stream.thumbnailUrl ?? '').isNotEmpty)
                    ClipRRect(
                      borderRadius: const BorderRadius.vertical(
                        top: Radius.circular(16),
                      ),
                      child: Image.network(
                        stream.thumbnailUrl!,
                        fit: BoxFit.cover,
                        width: double.infinity,
                        height: double.infinity,
                        errorBuilder: (_, _, _) => const SizedBox.shrink(),
                      ),
                    )
                  else
                    const Center(
                      child: Icon(
                        Icons.play_circle_outline,
                        size: 56,
                        color: Colors.white60,
                      ),
                    ),
                  Positioned(
                    top: 10,
                    left: 10,
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 8,
                        vertical: 4,
                      ),
                      decoration: BoxDecoration(
                        color: AppColors.liveRed,
                        borderRadius: BorderRadius.circular(999),
                      ),
                      child: Text(
                        'LIVE',
                        style: AppTextStyles.labelSmall.copyWith(
                          color: Colors.white,
                        ),
                      ),
                    ),
                  ),
                  Positioned(
                    top: 10,
                    right: 10,
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 8,
                        vertical: 4,
                      ),
                      decoration: BoxDecoration(
                        color: Colors.black.withValues(alpha: 0.45),
                        borderRadius: BorderRadius.circular(999),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(
                            Icons.visibility,
                            color: Colors.white,
                            size: 14,
                          ),
                          const SizedBox(width: 4),
                          Text(
                            _formatViewers(stream.totalViewers),
                            style: AppTextStyles.labelSmall.copyWith(
                              color: Colors.white,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 12, 12, 10),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  stream.title.isEmpty ? 'Untitled Stream' : stream.title,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyles.h3,
                ),
                const SizedBox(height: 2),
                Text(
                  'Host ${stream.hostId}  |  ${_timeAgo(stream.startedAt ?? stream.createdAt)}',
                  style: AppTextStyles.labelSmall,
                ),
                const SizedBox(height: 10),
                Row(
                  children: [
                    ElevatedButton.icon(
                      onPressed: joining ? null : onJoin,
                      style: ElevatedButton.styleFrom(
                        backgroundColor: AppColors.postbookPrimary,
                        foregroundColor: Colors.white,
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(10),
                        ),
                      ),
                      icon: joining
                          ? const SizedBox(
                              width: 12,
                              height: 12,
                              child: CircularProgressIndicator(
                                strokeWidth: 2,
                                color: Colors.white,
                              ),
                            )
                          : const Icon(Icons.play_arrow_rounded),
                      label: const Text('Watch'),
                    ),
                    const SizedBox(width: 10),
                    OutlinedButton.icon(
                      onPressed: liking ? null : onLike,
                      style: OutlinedButton.styleFrom(
                        foregroundColor: AppColors.textSecondary,
                        side: const BorderSide(color: AppColors.borderSubtle),
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(10),
                        ),
                      ),
                      icon: const Icon(Icons.favorite_border_rounded),
                      label: Text('${stream.likeCount}'),
                    ),
                    const Spacer(),
                    Text(
                      stream.visibility.toUpperCase(),
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.posttubePrimary,
                      ),
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

  String _formatViewers(int count) {
    if (count >= 1000000) {
      return '${(count / 1000000).toStringAsFixed(1)}M';
    }
    if (count >= 1000) {
      return '${(count / 1000).toStringAsFixed(1)}K';
    }
    return '$count';
  }

  String _timeAgo(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inDays > 0) return '${diff.inDays}d ago';
    if (diff.inHours > 0) return '${diff.inHours}h ago';
    if (diff.inMinutes > 0) return '${diff.inMinutes}m ago';
    return 'just now';
  }
}
