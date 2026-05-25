// Live-streaming v2: a grid of currently-live streams. Pulls from
// `liveStreamsListProvider` (live-service-v2). Tapping a card pushes
// `/live/v2/:id` which is the LiveKit-based viewer flow.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/live_stream_v2.dart';
import 'package:atpost_app/providers/live_streams_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class LiveListScreen extends ConsumerWidget {
  const LiveListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final pageAsync = ref.watch(liveStreamsListProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Row(
          children: [
            const Icon(Icons.radio_button_checked, color: AppColors.liveRed),
            const SizedBox(width: 8),
            Text('Live now', style: AppTextStyles.h2),
          ],
        ),
        actions: [
          IconButton(
            tooltip: 'Go Live',
            icon: const Icon(Icons.video_call, color: AppColors.liveRed),
            onPressed: () => context.push('/live/v2/new'),
          ),
        ],
      ),
      body: RefreshIndicator(
        color: AppColors.liveRed,
        onRefresh: () async {
          ref.invalidate(liveStreamsListProvider);
          await ref.read(liveStreamsListProvider.future);
        },
        child: pageAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppColors.liveRed),
          ),
          error: (err, _) => ListView(
            // single-child can't refresh via RefreshIndicator otherwise
            children: [
              const SizedBox(height: 80),
              Center(
                child: Text(
                  'Couldn\'t load live streams.',
                  style: AppTextStyles.body.copyWith(
                    color: AppColors.textSecondary,
                  ),
                ),
              ),
            ],
          ),
          data: (page) {
            if (page.items.isEmpty) {
              return ListView(
                physics: const AlwaysScrollableScrollPhysics(),
                children: [
                  const SizedBox(height: 120),
                  Icon(
                    Icons.radio,
                    size: 56,
                    color: AppColors.textTertiary.withValues(alpha: 0.4),
                  ),
                  const SizedBox(height: 12),
                  Center(
                    child: Text(
                      'No live streams right now.',
                      style: AppTextStyles.body
                          .copyWith(color: AppColors.textSecondary),
                    ),
                  ),
                  const SizedBox(height: 8),
                  Center(
                    child: TextButton.icon(
                      icon: const Icon(Icons.video_call,
                          color: AppColors.liveRed),
                      label: Text(
                        'Start your own broadcast',
                        style: AppTextStyles.labelSmall
                            .copyWith(color: AppColors.liveRed),
                      ),
                      onPressed: () => context.push('/live/v2/new'),
                    ),
                  ),
                ],
              );
            }
            return GridView.builder(
              padding: const EdgeInsets.all(12),
              gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
                maxCrossAxisExtent: 280,
                childAspectRatio: 0.85,
                mainAxisSpacing: 12,
                crossAxisSpacing: 12,
              ),
              itemCount: page.items.length,
              itemBuilder: (context, i) => _LiveCard(stream: page.items[i]),
            );
          },
        ),
      ),
    );
  }
}

class _LiveCard extends StatelessWidget {
  final LiveStreamV2 stream;
  const _LiveCard({required this.stream});

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(14),
      onTap: () => context.push('/live/v2/${stream.id}'),
      child: Container(
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        clipBehavior: Clip.antiAlias,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            AspectRatio(
              aspectRatio: 16 / 9,
              child: Stack(
                children: [
                  Positioned.fill(
                    child: Container(
                      decoration: const BoxDecoration(
                        gradient: LinearGradient(
                          begin: Alignment.topLeft,
                          end: Alignment.bottomRight,
                          colors: [
                            AppColors.postgramPrimary,
                            AppColors.accentPurple,
                          ],
                        ),
                      ),
                      child: const Center(
                        child: Icon(Icons.podcasts,
                            color: Colors.white, size: 36),
                      ),
                    ),
                  ),
                  Positioned(
                    left: 8,
                    top: 8,
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 8, vertical: 3),
                      decoration: BoxDecoration(
                        color: AppColors.liveRed,
                        borderRadius: BorderRadius.circular(20),
                      ),
                      child: Text(
                        'LIVE',
                        style: AppTextStyles.labelTiny.copyWith(
                          color: Colors.white,
                          fontWeight: FontWeight.w900,
                          letterSpacing: 1.5,
                        ),
                      ),
                    ),
                  ),
                  Positioned(
                    right: 8,
                    top: 8,
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 8, vertical: 3),
                      decoration: BoxDecoration(
                        color: Colors.black.withValues(alpha: 0.55),
                        borderRadius: BorderRadius.circular(20),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(Icons.remove_red_eye,
                              color: Colors.white, size: 12),
                          const SizedBox(width: 3),
                          Text(
                            stream.viewerPeak.toString(),
                            style: AppTextStyles.labelTiny.copyWith(
                              color: Colors.white,
                              fontWeight: FontWeight.bold,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.all(10),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    stream.title.isEmpty ? 'Live stream' : stream.title,
                    style: AppTextStyles.bodyMedium
                        .copyWith(color: AppColors.textPrimary),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 4),
                  Text(
                    _visibilityLabel(stream.visibility),
                    style: AppTextStyles.labelTiny
                        .copyWith(color: AppColors.textTertiary),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  String _visibilityLabel(LiveStreamVisibility vis) {
    switch (vis) {
      case LiveStreamVisibility.public:
        return 'Public';
      case LiveStreamVisibility.followers:
        return 'Followers only';
      case LiveStreamVisibility.paid:
        return 'Paid stream';
      case LiveStreamVisibility.unknown:
        return '';
    }
  }
}
