import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class QuestionCard extends ConsumerWidget {
  final Question question;
  final VoidCallback? onTap;

  const QuestionCard({
    super.key,
    required this.question,
    this.onTap,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return RepaintBoundary(
      child: GestureDetector(
        onTap: onTap,
        child: Container(
          margin: const EdgeInsets.only(bottom: 12),
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _buildHeader(),
              const SizedBox(height: 12),
              Text(
                question.title,
                style: AppTextStyles.h2.copyWith(fontSize: 18),
              ),
              const SizedBox(height: 8),
              Text(
                question.body,
                maxLines: 3,
                overflow: TextOverflow.ellipsis,
                style: AppTextStyles.body.copyWith(color: AppColors.textSecondary),
              ),
              if (question.topics.isNotEmpty) _buildTopics(),
              const SizedBox(height: 16),
              _buildFooter(ref),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildHeader() {
    return Row(
      children: [
        CircleAvatar(
          radius: 14,
          backgroundColor: AppColors.bgTertiary,
          backgroundImage: question.authorAvatar != null ? NetworkImage(question.authorAvatar!) : null,
          child: question.authorAvatar == null
              ? Text(question.authorName?[0] ?? '?', style: AppTextStyles.labelSmall)
              : null,
        ),
        const SizedBox(width: 8),
        Text(
          question.authorName ?? 'Anonymous',
          style: AppTextStyles.labelSmall.copyWith(color: AppColors.textSecondary),
        ),
        const Spacer(),
        if (question.communityId != null)
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            decoration: BoxDecoration(
              color: AppColors.posttubePrimary.withOpacity(0.1),
              borderRadius: BorderRadius.circular(8),
            ),
            child: Text(
              'Community',
              style: AppTextStyles.labelTiny.copyWith(color: AppColors.posttubePrimary),
            ),
          ),
      ],
    );
  }

  Widget _buildTopics() {
    return Padding(
      padding: const EdgeInsets.only(top: 12),
      child: Wrap(
        spacing: 8,
        children: question.topics.map((topic) => Text(
          '#$topic',
          style: AppTextStyles.tag.copyWith(color: AppColors.postbookPrimary),
        )).toList(),
      ),
    );
  }

  Widget _buildFooter(WidgetRef ref) {
    final notifier = ref.read(qaFeedProvider.notifier);

    return Row(
      children: [
        _VoteBadge(
          score: question.voteScore,
          viewerVote: question.viewerVote,
          onUpvote: () => notifier.updateVote(question.id, 1),
          onDownvote: () => notifier.updateVote(question.id, -1),
        ),
        const SizedBox(width: 16),
        Icon(Icons.chat_bubble_outline, size: 18, color: AppColors.textMuted),
        const SizedBox(width: 6),
        Text(
          '${question.answerCount} answers',
          style: AppTextStyles.labelSmall.copyWith(color: AppColors.textMuted),
        ),
        const Spacer(),
        Icon(Icons.share_outlined, size: 18, color: AppColors.textMuted),
      ],
    );
  }
}

class _VoteBadge extends StatelessWidget {
  final int score;
  final bool? viewerVote;
  final VoidCallback onUpvote;
  final VoidCallback onDownvote;

  const _VoteBadge({
    required this.score,
    this.viewerVote,
    required this.onUpvote,
    required this.onDownvote,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: Colors.white.withOpacity(0.05),
        borderRadius: BorderRadius.circular(20),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          IconButton(
            icon: Icon(
              Icons.arrow_upward_rounded,
              size: 18,
              color: viewerVote == true ? AppColors.postbookPrimary : AppColors.textMuted,
            ),
            onPressed: onUpvote,
            constraints: const BoxConstraints(),
            padding: const EdgeInsets.all(8),
          ),
          Text(
            '$score',
            style: AppTextStyles.label.copyWith(
              fontWeight: FontWeight.bold,
              color: viewerVote != null ? AppColors.postbookPrimary : Colors.white,
            ),
          ),
          IconButton(
            icon: Icon(
              Icons.arrow_downward_rounded,
              size: 18,
              color: viewerVote == false ? Colors.blueAccent : AppColors.textMuted,
            ),
            onPressed: onDownvote,
            constraints: const BoxConstraints(),
            padding: const EdgeInsets.all(8),
          ),
        ],
      ),
    );
  }
}
