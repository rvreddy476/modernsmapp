import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:flutter/material.dart';

class QaQuestionTile extends StatelessWidget {
  final QaQuestionSummary question;
  final VoidCallback? onTap;

  const QaQuestionTile({super.key, required this.question, this.onTap});

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        child: Ink(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Wrap(
                spacing: 8,
                runSpacing: 8,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  if (question.community != null)
                    _MetaChip(
                      label: question.community!.name,
                      color: AppColors.posttubePrimary,
                    ),
                  if (question.isPinned)
                    const _MetaChip(
                      label: 'Pinned',
                      color: AppColors.postbookPrimary,
                    ),
                  _MetaChip(
                    label: question.isAnswered ? 'Answered' : 'Open',
                    color: question.isAnswered
                        ? AppColors.statusSuccess
                        : AppColors.accentPurple,
                  ),
                ],
              ),
              const SizedBox(height: 10),
              Text(question.title, style: AppTextStyles.h2),
              if (question.excerpt.trim().isNotEmpty) ...[
                const SizedBox(height: 6),
                Text(
                  question.excerpt.trim(),
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textSecondary,
                    height: 1.45,
                  ),
                  maxLines: 3,
                  overflow: TextOverflow.ellipsis,
                ),
              ],
              const SizedBox(height: 12),
              Row(
                children: [
                  _Stat(
                    icon: Icons.arrow_upward_rounded,
                    value: question.voteScore,
                  ),
                  const SizedBox(width: 14),
                  _Stat(
                    icon: Icons.forum_outlined,
                    value: question.answerCount,
                  ),
                  const SizedBox(width: 14),
                  _Stat(
                    icon: Icons.visibility_outlined,
                    value: question.viewCount,
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
            ],
          ),
        ),
      ),
    );
  }
}

class _MetaChip extends StatelessWidget {
  final String label;
  final Color color;

  const _MetaChip({required this.label, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        label,
        style: AppTextStyles.labelSmall.copyWith(color: color, fontSize: 10.5),
      ),
    );
  }
}

class _Stat extends StatelessWidget {
  final IconData icon;
  final int value;

  const _Stat({required this.icon, required this.value});

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 14, color: AppColors.textMuted),
        const SizedBox(width: 4),
        Text(
          value.toString(),
          style: AppTextStyles.labelSmall.copyWith(color: AppColors.textMuted),
        ),
      ],
    );
  }
}

String relativeQaTime(DateTime createdAt) {
  final diff = DateTime.now().difference(createdAt);
  if (diff.inDays > 0) return '${diff.inDays}d';
  if (diff.inHours > 0) return '${diff.inHours}h';
  if (diff.inMinutes > 0) return '${diff.inMinutes}m';
  return 'now';
}
