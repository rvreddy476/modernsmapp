import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/features/discover/qa_question_tile.dart';
import 'package:atpost_app/features/discover/topic_detail_screen.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class QuestionDetailScreen extends ConsumerWidget {
  final String questionId;

  const QuestionDetailScreen({super.key, required this.questionId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final questionAsync = ref.watch(qaQuestionDetailProvider(questionId));
    final answersAsync = ref.watch(
      qaQuestionAnswersProvider(
        QaQuestionAnswersParams(questionId: questionId),
      ),
    );

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        foregroundColor: AppColors.textPrimary,
        title: Text('Question', style: AppTextStyles.h3),
      ),
      body: questionAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) => Center(
          child: Text('Could not load question', style: AppTextStyles.body),
        ),
        data: (question) => RefreshIndicator(
          color: AppColors.postbookPrimary,
          onRefresh: () async {
            ref.invalidate(qaQuestionDetailProvider(questionId));
            ref.invalidate(
              qaQuestionAnswersProvider(
                QaQuestionAnswersParams(questionId: questionId),
              ),
            );
          },
          child: ListView(
            padding: AppSpacing.pagePadding.copyWith(bottom: 120, top: 12),
            children: [
              if (question.community != null)
                Padding(
                  padding: const EdgeInsets.only(bottom: 10),
                  child: Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      _LinkChip(
                        label: question.community!.name,
                        color: AppColors.posttubePrimary,
                        onTap: () => context.push(
                          '/communities/${question.community!.id}',
                        ),
                      ),
                      _LinkChip(
                        label: question.status,
                        color: question.isAnswered
                            ? AppColors.statusSuccess
                            : AppColors.accentPurple,
                      ),
                    ],
                  ),
                ),
              Text(question.title, style: AppTextStyles.h1),
              const SizedBox(height: 12),
              Text(
                _questionBody(question.body, question.bodyHtml),
                style: AppTextStyles.body.copyWith(
                  color: AppColors.textPrimary,
                  height: 1.65,
                ),
              ),
              const SizedBox(height: 16),
              if (question.topics.isNotEmpty)
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: question.topics
                      .map(
                        (topic) => _LinkChip(
                          label: '#${topic.slug}',
                          color: AppColors.postbookPrimary,
                          onTap: () {
                            Navigator.of(context).push(
                              MaterialPageRoute<void>(
                                builder: (_) =>
                                    TopicDetailScreen(topicId: topic.id),
                              ),
                            );
                          },
                        ),
                      )
                      .toList(),
                ),
              const SizedBox(height: 18),
              Container(
                padding: const EdgeInsets.all(14),
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                  border: Border.all(color: AppColors.borderSubtle),
                ),
                child: Row(
                  children: [
                    _QuestionStat(
                      label: 'Score',
                      value: question.voteScore.toString(),
                    ),
                    _QuestionStat(
                      label: 'Answers',
                      value: question.answerCount.toString(),
                    ),
                    _QuestionStat(
                      label: 'Views',
                      value: question.viewCount.toString(),
                    ),
                    const Spacer(),
                    Text(
                      relativeQaTime(question.createdAt),
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.textMuted,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 22),
              Text('Answers', style: AppTextStyles.h2),
              const SizedBox(height: 12),
              answersAsync.when(
                loading: () => const Padding(
                  padding: EdgeInsets.symmetric(vertical: 20),
                  child: Center(
                    child: CircularProgressIndicator(
                      color: AppColors.postbookPrimary,
                    ),
                  ),
                ),
                error: (_, _) => Padding(
                  padding: const EdgeInsets.symmetric(vertical: 20),
                  child: Center(
                    child: Text(
                      'Could not load answers',
                      style: AppTextStyles.body,
                    ),
                  ),
                ),
                data: (answers) {
                  if (answers.isEmpty) {
                    return Container(
                      padding: const EdgeInsets.all(18),
                      decoration: BoxDecoration(
                        color: AppColors.bgCard,
                        borderRadius: BorderRadius.circular(
                          AppSpacing.radiusLarge,
                        ),
                        border: Border.all(color: AppColors.borderSubtle),
                      ),
                      child: Text(
                        'No answers yet. This question is still waiting for the right contributor.',
                        style: AppTextStyles.body,
                      ),
                    );
                  }

                  return Column(
                    children: answers
                        .map(
                          (answer) => Padding(
                            padding: const EdgeInsets.only(bottom: 12),
                            child: _AnswerCard(answer: answer),
                          ),
                        )
                        .toList(),
                  );
                },
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _LinkChip extends StatelessWidget {
  final String label;
  final Color color;
  final VoidCallback? onTap;

  const _LinkChip({required this.label, required this.color, this.onTap});

  @override
  Widget build(BuildContext context) {
    final child = Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(label, style: AppTextStyles.label.copyWith(color: color)),
    );

    if (onTap == null) {
      return child;
    }

    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(999),
      child: child,
    );
  }
}

class _QuestionStat extends StatelessWidget {
  final String label;
  final String value;

  const _QuestionStat({required this.label, required this.value});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(right: 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            value,
            style: AppTextStyles.h3.copyWith(color: AppColors.textPrimary),
          ),
          Text(label, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}

class _AnswerCard extends StatelessWidget {
  final QaAnswer answer;

  const _AnswerCard({required this.answer});

  @override
  Widget build(BuildContext context) {
    final body = _questionBody(answer.body, answer.bodyHtml);

    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(
          color: answer.isBest == true
              ? AppColors.statusSuccess.withValues(alpha: 0.35)
              : AppColors.borderSubtle,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              if (answer.isBest == true)
                const _LinkChip(
                  label: 'Best answer',
                  color: AppColors.statusSuccess,
                ),
              _LinkChip(
                label: '${answer.voteScore} votes',
                color: AppColors.postbookPrimary,
              ),
            ],
          ),
          const SizedBox(height: 10),
          Text(
            body,
            style: AppTextStyles.body.copyWith(
              color: AppColors.textPrimary,
              height: 1.6,
            ),
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              Text(
                '${answer.commentCount} comments',
                style: AppTextStyles.labelSmall,
              ),
              const Spacer(),
              Text(
                relativeQaTime(answer.createdAt),
                style: AppTextStyles.labelSmall.copyWith(
                  color: AppColors.textMuted,
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

String _questionBody(String body, String bodyHtml) {
  final value = body.trim().isNotEmpty ? body : bodyHtml;
  return value.replaceAll(RegExp(r'<[^>]*>'), '').trim();
}
