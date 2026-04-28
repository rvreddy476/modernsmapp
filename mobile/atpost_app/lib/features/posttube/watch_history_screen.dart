import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Watch history / continue-watching screen.
///
/// Backed by `GET /v1/videos/continue-watching`, which returns the most
/// recently played videos with the user's resume position so the UI can
/// show a percent-watched progress bar.
final _continueWatchingFutureProvider =
    FutureProvider.autoDispose<List<_HistoryEntry>>((ref) async {
  final api = ref.watch(apiClientProvider);
  final res = await api.get('/v1/videos/continue-watching');
  final raw = res.data;
  final list = (raw is Map && raw['data'] is List)
      ? raw['data'] as List
      : (raw is List ? raw : const []);
  return list
      .whereType<Map>()
      .map((e) => _HistoryEntry.fromJson(Map<String, dynamic>.from(e)))
      .toList();
});

class WatchHistoryScreen extends ConsumerWidget {
  const WatchHistoryScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(_continueWatchingFutureProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        title: const Text('Watch history'),
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
      ),
      body: RefreshIndicator(
        onRefresh: () async => ref.refresh(_continueWatchingFutureProvider),
        child: async.when(
          data: (entries) {
            if (entries.isEmpty) {
              return ListView(
                children: [
                  const SizedBox(height: 80),
                  Center(
                    child: Padding(
                      padding: const EdgeInsets.all(24),
                      child: Text(
                        'Nothing here yet.\nVideos you start watching will show up so you can pick up where you left off.',
                        textAlign: TextAlign.center,
                        style: AppTextStyles.bodySmall.copyWith(
                          color: AppColors.textDim,
                        ),
                      ),
                    ),
                  ),
                ],
              );
            }
            return ListView.separated(
              padding: AppSpacing.pagePadding,
              itemCount: entries.length,
              separatorBuilder: (_, _) => const SizedBox(height: 12),
              itemBuilder: (_, i) => _HistoryRow(entry: entries[i]),
            );
          },
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, _) => Center(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Text(
                'Could not load watch history.',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textDim,
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _HistoryEntry {
  const _HistoryEntry({
    required this.postId,
    required this.title,
    required this.thumbnailUrl,
    required this.percent,
  });

  final String postId;
  final String title;
  final String? thumbnailUrl;
  final double percent;

  factory _HistoryEntry.fromJson(Map<String, dynamic> json) {
    return _HistoryEntry(
      postId: (json['post_id'] ?? json['id'] ?? '').toString(),
      title: (json['title'] ?? json['text'] ?? '').toString(),
      thumbnailUrl: (json['thumbnail_url'] ?? json['cover_url']) as String?,
      percent: ((json['percent_watched'] ?? json['progress_pct']) as num?)
              ?.toDouble() ??
          0,
    );
  }
}

class _HistoryRow extends StatelessWidget {
  const _HistoryRow({required this.entry});
  final _HistoryEntry entry;

  @override
  Widget build(BuildContext context) {
    final thumb = (entry.thumbnailUrl ?? '').isNotEmpty
        ? '${Environment.apiBaseUrl}${entry.thumbnailUrl}'
        : null;
    final pct = entry.percent.clamp(0, 100).toDouble();
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          Row(
            children: [
              ClipRRect(
                borderRadius: const BorderRadius.horizontal(
                  left: Radius.circular(20),
                ),
                child: SizedBox(
                  width: 130,
                  height: 80,
                  child: thumb != null
                      ? Image.network(
                          thumb,
                          fit: BoxFit.cover,
                          errorBuilder: (_, _, _) =>
                              Container(color: AppColors.bgTertiary),
                        )
                      : Container(color: AppColors.bgTertiary),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Padding(
                  padding: const EdgeInsets.symmetric(vertical: 10),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      Text(
                        entry.title.isEmpty ? 'Untitled' : entry.title,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                        style: AppTextStyles.label,
                      ),
                      const SizedBox(height: 4),
                      Text(
                        '${pct.toStringAsFixed(0)}% watched',
                        style: AppTextStyles.monoSmall.copyWith(
                          color: AppColors.textDim,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
              const SizedBox(width: 12),
            ],
          ),
          // Resume progress bar across the bottom edge.
          ClipRRect(
            borderRadius: const BorderRadius.vertical(
              bottom: Radius.circular(20),
            ),
            child: LinearProgressIndicator(
              value: pct / 100.0,
              minHeight: 3,
              backgroundColor: AppColors.bgTertiary,
              color: AppColors.posttubePrimary,
            ),
          ),
        ],
      ),
    );
  }
}
