import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/live_stream.dart';
import 'package:atpost_app/data/repositories/live_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final liveStreamsProvider = FutureProvider.autoDispose<List<LiveStream>>((ref) async {
  final repo = ref.watch(liveRepositoryProvider);
  return repo.getLiveStreams();
});

class LiveScreen extends ConsumerWidget {
  const LiveScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final streamsAsync = ref.watch(liveStreamsProvider);

    return SafeArea(
      child: Padding(
        padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 100),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Row(
                  children: [
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                      decoration: BoxDecoration(
                        color: Colors.red,
                        borderRadius: BorderRadius.circular(4),
                      ),
                      child: const Text('LIVE', style: TextStyle(color: Colors.white, fontSize: 12, fontWeight: FontWeight.bold)),
                    ),
                    const SizedBox(width: 10),
                    Text('Live Streams', style: AppTextStyles.h1),
                  ],
                ),
                ElevatedButton.icon(
                  onPressed: () => _showGoLiveSheet(context),
                  icon: const Icon(Icons.videocam, size: 18),
                  label: const Text('Go Live'),
                  style: ElevatedButton.styleFrom(
                    backgroundColor: Colors.red,
                    foregroundColor: Colors.white,
                    shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(20)),
                    padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 16),

            Expanded(
              child: streamsAsync.when(
                data: (streams) {
                  if (streams.isEmpty) {
                    return Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(Icons.live_tv, color: AppColors.textDim, size: 54),
                          const SizedBox(height: 12),
                          Text('No live streams right now', style: AppTextStyles.body.copyWith(color: AppColors.textDim)),
                          const SizedBox(height: 8),
                          Text('Be the first to go live!', style: AppTextStyles.labelSmall.copyWith(color: AppColors.textDim)),
                        ],
                      ),
                    );
                  }
                  return ListView.separated(
                    itemCount: streams.length,
                    separatorBuilder: (_, _) => const SizedBox(height: 12),
                    itemBuilder: (context, index) => _LiveStreamCard(stream: streams[index]),
                  );
                },
                loading: () => const Center(child: CircularProgressIndicator()),
                error: (_, _) => Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Icon(Icons.live_tv, color: AppColors.textDim, size: 54),
                      const SizedBox(height: 12),
                      Text('Live streaming coming soon', style: AppTextStyles.body.copyWith(color: AppColors.textDim)),
                    ],
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  void _showGoLiveSheet(BuildContext context) {
    final titleController = TextEditingController();

    showModalBottomSheet(
      context: context,
      backgroundColor: AppColors.bgCard,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (context) => Padding(
        padding: EdgeInsets.only(
          left: 20,
          right: 20,
          top: 20,
          bottom: MediaQuery.of(context).viewInsets.bottom + 20,
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Start a Live Stream', style: AppTextStyles.h2),
            const SizedBox(height: 16),
            TextField(
              controller: titleController,
              decoration: InputDecoration(
                hintText: 'Stream title...',
                filled: true,
                fillColor: AppColors.bgPrimary,
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(12),
                  borderSide: BorderSide.none,
                ),
              ),
            ),
            const SizedBox(height: 16),
            SizedBox(
              width: double.infinity,
              child: ElevatedButton(
                onPressed: () {
                  Navigator.pop(context);
                },
                style: ElevatedButton.styleFrom(
                  backgroundColor: Colors.red,
                  foregroundColor: Colors.white,
                  shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
                  padding: const EdgeInsets.symmetric(vertical: 14),
                ),
                child: const Text('Go Live Now'),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _LiveStreamCard extends StatelessWidget {
  final LiveStream stream;

  const _LiveStreamCard({required this.stream});

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Thumbnail / video preview area
          AspectRatio(
            aspectRatio: 16 / 9,
            child: Container(
              decoration: BoxDecoration(
                color: Colors.black,
                borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
              ),
              child: Stack(
                children: [
                  if (stream.thumbnailUrl != null)
                    ClipRRect(
                      borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
                      child: Image.network(stream.thumbnailUrl!, fit: BoxFit.cover, width: double.infinity, height: double.infinity),
                    )
                  else
                    const Center(child: Icon(Icons.play_circle_outline, color: Colors.white38, size: 64)),

                  // LIVE badge
                  Positioned(
                    top: 10,
                    left: 10,
                    child: Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                      decoration: BoxDecoration(
                        color: Colors.red,
                        borderRadius: BorderRadius.circular(4),
                      ),
                      child: const Text('LIVE', style: TextStyle(color: Colors.white, fontSize: 11, fontWeight: FontWeight.bold)),
                    ),
                  ),

                  // Viewer count
                  Positioned(
                    top: 10,
                    right: 10,
                    child: Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                      decoration: BoxDecoration(
                        color: Colors.black54,
                        borderRadius: BorderRadius.circular(4),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(Icons.visibility, color: Colors.white, size: 14),
                          const SizedBox(width: 4),
                          Text(
                            _formatViewers(stream.totalViewers),
                            style: const TextStyle(color: Colors.white, fontSize: 12),
                          ),
                        ],
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),

          // Stream info
          Padding(
            padding: const EdgeInsets.all(12),
            child: Row(
              children: [
                // Host avatar
                const CircleAvatar(
                  radius: 18,
                  backgroundColor: AppColors.bgPrimary,
                  child: Icon(Icons.person, color: AppColors.textDim, size: 20),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        stream.title.isNotEmpty ? stream.title : 'Untitled Stream',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: AppTextStyles.body.copyWith(fontWeight: FontWeight.w600),
                      ),
                      if (stream.startedAt != null)
                        Text(
                          'Started ${_timeAgo(stream.startedAt!)}',
                          style: AppTextStyles.labelSmall.copyWith(color: AppColors.textDim),
                        ),
                    ],
                  ),
                ),
                // Like count
                Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(Icons.favorite, color: Colors.red, size: 16),
                    const SizedBox(width: 4),
                    Text('${stream.likeCount}', style: AppTextStyles.labelSmall),
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
    if (count >= 1000) {
      return '${(count / 1000).toStringAsFixed(1)}K';
    }
    return '$count';
  }

  String _timeAgo(DateTime time) {
    final diff = DateTime.now().difference(time);
    if (diff.inMinutes < 1) return 'just now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    if (diff.inHours < 24) return '${diff.inHours}h ago';
    return '${diff.inDays}d ago';
  }
}
