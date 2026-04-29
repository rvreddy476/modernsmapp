import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/data/repositories/qa_repository.dart';
import 'package:atpost_app/features/qa/ask_question_screen.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class QaDraftsScreen extends ConsumerWidget {
  const QaDraftsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final questionDraftsAsync = ref.watch(qaQuestionDraftsProvider);
    final answerDraftsAsync = ref.watch(qaAnswerDraftsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: GlassIconButton(
          icon: Icons.arrow_back_ios_new,
          tooltip: 'Back',
          onPressed: () => context.pop(),
        ),
        title: Text('Drafts', style: AppTextStyles.h2),
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Text('Question drafts', style: AppTextStyles.h3),
          const SizedBox(height: 8),
          questionDraftsAsync.when(
            loading: () => const Center(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: CircularProgressIndicator(
                    color: AppColors.postbookPrimary),
              ),
            ),
            error: (_, _) => Text(
              'Could not load drafts.',
              style: AppTextStyles.body.copyWith(color: AppColors.textDim),
            ),
            data: (drafts) {
              if (drafts.isEmpty) {
                return Padding(
                  padding: const EdgeInsets.symmetric(vertical: 12),
                  child: Text(
                    'No question drafts saved.',
                    style:
                        AppTextStyles.body.copyWith(color: AppColors.textDim),
                  ),
                );
              }
              return Column(
                children: drafts
                    .map((d) => _QuestionDraftTile(draft: d))
                    .toList(),
              );
            },
          ),
          const SizedBox(height: 24),
          Text('Answer drafts', style: AppTextStyles.h3),
          const SizedBox(height: 8),
          answerDraftsAsync.when(
            loading: () => const Center(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: CircularProgressIndicator(
                    color: AppColors.postbookPrimary),
              ),
            ),
            error: (_, _) => Text(
              'Could not load drafts.',
              style: AppTextStyles.body.copyWith(color: AppColors.textDim),
            ),
            data: (drafts) {
              if (drafts.isEmpty) {
                return Padding(
                  padding: const EdgeInsets.symmetric(vertical: 12),
                  child: Text(
                    'No answer drafts saved.',
                    style:
                        AppTextStyles.body.copyWith(color: AppColors.textDim),
                  ),
                );
              }
              return Column(
                children:
                    drafts.map((d) => _AnswerDraftTile(draft: d)).toList(),
              );
            },
          ),
        ],
      ),
    );
  }
}

class _QuestionDraftTile extends ConsumerWidget {
  final QuestionDraft draft;
  const _QuestionDraftTile({required this.draft});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Card(
      color: AppColors.bgCard,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: const BorderSide(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        title: Text(
          draft.title.isEmpty ? '(untitled)' : draft.title,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: AppTextStyles.body,
        ),
        subtitle: Text(
          draft.body,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: AppTextStyles.labelSmall
              .copyWith(color: AppColors.textSecondary),
        ),
        trailing: IconButton(
          icon: const Icon(Icons.delete_outline, color: AppColors.statusError),
          onPressed: () async {
            if (draft.id == null) return;
            try {
              await ref
                  .read(qaRepositoryProvider)
                  .deleteQuestionDraft(draft.id!);
              ref.invalidate(qaQuestionDraftsProvider);
            } catch (_) {
              if (context.mounted) {
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(content: Text('Could not delete draft.')),
                );
              }
            }
          },
        ),
        onTap: () {
          Navigator.of(context).push(
            MaterialPageRoute<void>(
              builder: (_) => AskQuestionScreen(
                draftId: draft.id,
                initialTitle: draft.title,
                initialBody: draft.body,
                initialCommunityId: draft.communityId,
                initialTopics: draft.tags,
                initialIsAnonymous: draft.isAnonymous,
              ),
            ),
          );
        },
      ),
    );
  }
}

class _AnswerDraftTile extends ConsumerWidget {
  final AnswerDraft draft;
  const _AnswerDraftTile({required this.draft});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Card(
      color: AppColors.bgCard,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: const BorderSide(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        title: Text(
          draft.body.isEmpty ? '(empty)' : draft.body,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: AppTextStyles.body,
        ),
        subtitle: Text(
          'Question: ${draft.questionId}',
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: AppTextStyles.labelSmall
              .copyWith(color: AppColors.textSecondary),
        ),
        trailing: IconButton(
          icon: const Icon(Icons.delete_outline, color: AppColors.statusError),
          onPressed: () async {
            if (draft.id == null) return;
            try {
              await ref
                  .read(qaRepositoryProvider)
                  .deleteAnswerDraft(draft.id!);
              ref.invalidate(qaAnswerDraftsProvider);
            } catch (_) {
              if (context.mounted) {
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(content: Text('Could not delete draft.')),
                );
              }
            }
          },
        ),
        onTap: () {
          context.push('/qa/question/${draft.questionId}');
        },
      ),
    );
  }
}
