import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:atpost_app/features/discover/topic_detail_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class QaTopicsScreen extends ConsumerWidget {
  const QaTopicsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final topicsAsync = ref.watch(qaTopicsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        foregroundColor: AppColors.textPrimary,
        title: Text('Topics', style: AppTextStyles.h2),
      ),
      body: topicsAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) =>
            _TopicsError(onRetry: () => ref.invalidate(qaTopicsProvider)),
        data: (topics) => RefreshIndicator(
          color: AppColors.postbookPrimary,
          onRefresh: () async => ref.invalidate(qaTopicsProvider),
          child: ListView(
            padding: AppSpacing.pagePadding.copyWith(bottom: 120, top: 16),
            children: [
              Container(
                padding: const EdgeInsets.all(18),
                decoration: BoxDecoration(
                  gradient: const LinearGradient(
                    colors: [AppColors.postbookPrimary, AppColors.accentPurple],
                    begin: Alignment.topLeft,
                    end: Alignment.bottomRight,
                  ),
                  borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'Topics organize Q&A across the app.',
                      style: AppTextStyles.h2.copyWith(color: Colors.white),
                    ),
                    const SizedBox(height: 8),
                    Text(
                      'Browse a topic to see cross-community questions, then drill into community-specific threads from there.',
                      style: AppTextStyles.body.copyWith(
                        color: Colors.white.withValues(alpha: 0.88),
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 20),
              if (topics.isEmpty)
                Center(
                  child: Padding(
                    padding: const EdgeInsets.only(top: 32),
                    child: Text(
                      'No topics available yet.',
                      style: AppTextStyles.body,
                    ),
                  ),
                )
              else
                ...topics.map(
                  (topic) => Padding(
                    padding: const EdgeInsets.only(bottom: 12),
                    child: _TopicTile(topic: topic),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class _TopicTile extends StatelessWidget {
  final QaTopic topic;

  const _TopicTile({required this.topic});

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: () {
          Navigator.of(context).push(
            MaterialPageRoute<void>(
              builder: (_) => TopicDetailScreen(topicId: topic.id),
            ),
          );
        },
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        child: Ink(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            children: [
              Container(
                width: 48,
                height: 48,
                decoration: BoxDecoration(
                  gradient: topic.isFeatured
                      ? AppColors.postgramGradient
                      : AppColors.posttubeGradient,
                  borderRadius: BorderRadius.circular(16),
                ),
                child: Center(
                  child: Text(
                    topic.name.isNotEmpty ? topic.name[0].toUpperCase() : '#',
                    style: AppTextStyles.h2.copyWith(color: Colors.white),
                  ),
                ),
              ),
              const SizedBox(width: 14),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Flexible(
                          child: Text(topic.name, style: AppTextStyles.h3),
                        ),
                        if (topic.isFeatured) ...[
                          const SizedBox(width: 6),
                          const Icon(
                            Icons.auto_awesome,
                            size: 14,
                            color: AppColors.postbookPrimary,
                          ),
                        ],
                      ],
                    ),
                    const SizedBox(height: 4),
                    Text(
                      '#${topic.slug}',
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.posttubePrimary,
                      ),
                    ),
                    if (topic.description.trim().isNotEmpty) ...[
                      const SizedBox(height: 6),
                      Text(
                        topic.description,
                        style: AppTextStyles.bodySmall,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ],
                    const SizedBox(height: 8),
                    Wrap(
                      spacing: 10,
                      runSpacing: 6,
                      children: [
                        _TopicStat(
                          icon: Icons.help_outline,
                          label: '${topic.questionCount} questions',
                        ),
                        _TopicStat(
                          icon: Icons.people_outline,
                          label: '${topic.followerCount} following',
                        ),
                      ],
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 8),
              const Icon(
                Icons.arrow_forward_ios_rounded,
                size: 14,
                color: AppColors.textMuted,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _TopicStat extends StatelessWidget {
  final IconData icon;
  final String label;

  const _TopicStat({required this.icon, required this.label});

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 14, color: AppColors.textMuted),
        const SizedBox(width: 4),
        Text(
          label,
          style: AppTextStyles.labelSmall.copyWith(color: AppColors.textMuted),
        ),
      ],
    );
  }
}

class _TopicsError extends StatelessWidget {
  final VoidCallback onRetry;

  const _TopicsError({required this.onRetry});

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.error_outline, color: AppColors.textDim, size: 44),
            const SizedBox(height: 12),
            Text('Could not load topics', style: AppTextStyles.h3),
            const SizedBox(height: 8),
            TextButton(onPressed: onRetry, child: const Text('Retry')),
          ],
        ),
      ),
    );
  }
}
