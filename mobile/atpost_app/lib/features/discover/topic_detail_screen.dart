import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/qa/question_detail_screen.dart';
import 'package:atpost_app/features/discover/qa_question_tile.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class TopicDetailScreen extends ConsumerStatefulWidget {
  final String topicId;

  const TopicDetailScreen({super.key, required this.topicId});

  @override
  ConsumerState<TopicDetailScreen> createState() => _TopicDetailScreenState();
}

class _TopicDetailScreenState extends ConsumerState<TopicDetailScreen> {
  String _sort = 'recent';

  @override
  Widget build(BuildContext context) {
    final topicAsync = ref.watch(qaTopicProvider(widget.topicId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        foregroundColor: AppColors.textPrimary,
      ),
      body: topicAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) => Center(
          child: Text('Could not load topic', style: AppTextStyles.body),
        ),
        data: (topic) {
          final questionsAsync = ref.watch(
            qaTopicQuestionsProvider(
              QaTopicQuestionsParams(topicSlug: topic.slug, sort: _sort),
            ),
          );

          return RefreshIndicator(
            color: AppColors.postbookPrimary,
            onRefresh: () async {
              ref.invalidate(qaTopicProvider(widget.topicId));
              ref.invalidate(
                qaTopicQuestionsProvider(
                  QaTopicQuestionsParams(topicSlug: topic.slug, sort: _sort),
                ),
              );
            },
            child: ListView(
              padding: AppSpacing.pagePadding.copyWith(bottom: 120, top: 12),
              children: [
                Container(
                  padding: const EdgeInsets.all(18),
                  decoration: BoxDecoration(
                    gradient: topic.isFeatured
                        ? AppColors.postgramGradient
                        : AppColors.posttubeGradient,
                    borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        topic.name,
                        style: AppTextStyles.h1.copyWith(color: Colors.white),
                      ),
                      const SizedBox(height: 6),
                      Text(
                        '#${topic.slug}',
                        style: AppTextStyles.label.copyWith(
                          color: Colors.white.withValues(alpha: 0.86),
                        ),
                      ),
                      if (topic.description.trim().isNotEmpty) ...[
                        const SizedBox(height: 12),
                        Text(
                          topic.description,
                          style: AppTextStyles.body.copyWith(
                            color: Colors.white.withValues(alpha: 0.88),
                          ),
                        ),
                      ],
                      const SizedBox(height: 14),
                      Wrap(
                        spacing: 10,
                        runSpacing: 8,
                        children: [
                          _HeaderPill(
                            label: '${topic.questionCount} questions',
                          ),
                          _HeaderPill(
                            label: '${topic.followerCount} following',
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
                const SizedBox(height: 18),
                Row(
                  children: [
                    Text('Questions', style: AppTextStyles.h2),
                    const Spacer(),
                    _SortChip(
                      label: 'Recent',
                      selected: _sort == 'recent',
                      onTap: () => setState(() => _sort = 'recent'),
                    ),
                    const SizedBox(width: 8),
                    _SortChip(
                      label: 'Top',
                      selected: _sort == 'votes',
                      onTap: () => setState(() => _sort = 'votes'),
                    ),
                  ],
                ),
                const SizedBox(height: 12),
                questionsAsync.when(
                  loading: () => const Padding(
                    padding: EdgeInsets.symmetric(vertical: 32),
                    child: Center(
                      child: CircularProgressIndicator(
                        color: AppColors.postbookPrimary,
                      ),
                    ),
                  ),
                  error: (_, _) => Padding(
                    padding: const EdgeInsets.symmetric(vertical: 32),
                    child: Center(
                      child: Text(
                        'Could not load questions for this topic',
                        style: AppTextStyles.body,
                      ),
                    ),
                  ),
                  data: (questions) {
                    if (questions.isEmpty) {
                      return Padding(
                        padding: const EdgeInsets.symmetric(vertical: 32),
                        child: Center(
                          child: Text(
                            'No questions tagged with this topic yet.',
                            style: AppTextStyles.body,
                          ),
                        ),
                      );
                    }

                    return Column(
                      children: questions
                          .map(
                            (question) => Padding(
                              padding: const EdgeInsets.only(bottom: 12),
                              child: QaQuestionTile(
                                question: question.toSummary(),
                                onTap: () {
                                  Navigator.of(context).push(
                                    MaterialPageRoute<void>(
                                      builder: (_) => QuestionDetailScreen(
                                        questionId: question.id,
                                      ),
                                    ),
                                  );
                                },
                              ),
                            ),
                          )
                          .toList(),
                    );
                  },
                ),
              ],
            ),
          );
        },
      ),
    );
  }
}

class _SortChip extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;

  const _SortChip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(999),
      child: Ink(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(
          color: selected
              ? AppColors.postbookPrimary.withValues(alpha: 0.16)
              : AppColors.bgCard,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.label.copyWith(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.textSecondary,
          ),
        ),
      ),
    );
  }
}

class _HeaderPill extends StatelessWidget {
  final String label;

  const _HeaderPill({required this.label});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
      decoration: BoxDecoration(
        color: Colors.white.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        label,
        style: AppTextStyles.label.copyWith(color: Colors.white),
      ),
    );
  }
}
