import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:flutter/material.dart';

/// Sprint 2 — List mode for the Pulse hero surface.
///
/// Vertical `ListView.builder` of candidate rows. Tap a row to open the
/// detail sheet; swipe right (Dismissible) to spark, swipe left to pass.
/// Two action chips on each row mirror the swipes for accessibility.
class PulseListView extends StatelessWidget {
  const PulseListView({
    super.key,
    required this.cards,
    required this.onCardOpen,
    required this.onSpark,
    required this.onStash,
    required this.onPass,
    required this.onRefresh,
  });

  final List<PulseCard> cards;
  final ValueChanged<PulseCard> onCardOpen;
  final ValueChanged<PulseCard> onSpark;
  final ValueChanged<PulseCard> onStash;
  final ValueChanged<PulseCard> onPass;
  final Future<void> Function() onRefresh;

  @override
  Widget build(BuildContext context) {
    if (cards.isEmpty) {
      return RefreshIndicator(
        color: AppColors.postbookPrimary,
        onRefresh: onRefresh,
        child: ListView(
          padding: AppSpacing.pagePadding.copyWith(top: 24),
          children: [
            const SizedBox(height: 80),
            Text(
              'Your pulse is quiet right now.',
              style: AppTextStyles.h2,
              textAlign: TextAlign.center,
            ),
            const SizedBox(height: 8),
            Text(
              'New candidates show up as the day unfolds.',
              style: AppTextStyles.bodySmall,
              textAlign: TextAlign.center,
            ),
          ],
        ),
      );
    }

    return RefreshIndicator(
      color: AppColors.postbookPrimary,
      onRefresh: onRefresh,
      child: ListView.builder(
        padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 12),
        itemCount: cards.length,
        itemBuilder: (context, i) {
          final card = cards[i];
          return Dismissible(
            key: ValueKey('pulse-row-${card.candidateId}'),
            background: _swipeBg(
              align: Alignment.centerLeft,
              icon: Icons.bolt,
              color: AppColors.postbookPrimary,
              label: 'Spark',
            ),
            secondaryBackground: _swipeBg(
              align: Alignment.centerRight,
              icon: Icons.close,
              color: AppColors.statusError,
              label: 'Pass',
            ),
            onDismissed: (direction) {
              if (direction == DismissDirection.startToEnd) {
                onSpark(card);
              } else {
                onPass(card);
              }
            },
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 6),
              child: _PulseRow(
                card: card,
                onOpen: () => onCardOpen(card),
                onSpark: () => onSpark(card),
                onStash: () => onStash(card),
              ),
            ),
          );
        },
      ),
    );
  }

  Widget _swipeBg({
    required Alignment align,
    required IconData icon,
    required Color color,
    required String label,
  }) {
    return Container(
      alignment: align,
      padding: const EdgeInsets.symmetric(horizontal: 24),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.18),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, color: color),
          const SizedBox(width: 6),
          Text(
            label,
            style: AppTextStyles.label.copyWith(color: color),
          ),
        ],
      ),
    );
  }
}

class _PulseRow extends StatelessWidget {
  const _PulseRow({
    required this.card,
    required this.onOpen,
    required this.onSpark,
    required this.onStash,
  });

  final PulseCard card;
  final VoidCallback onOpen;
  final VoidCallback onSpark;
  final VoidCallback onStash;

  @override
  Widget build(BuildContext context) {
    final intentColor = _intentColor(card.profile.intent);
    final topReason =
        card.matchReasons.isNotEmpty ? card.matchReasons.first.summary : null;
    return InkWell(
      onTap: onOpen,
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          border: Border.all(color: AppColors.borderSubtle),
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        ),
        child: Row(
          children: [
            Container(
              width: 56,
              height: 56,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                border: Border.all(color: intentColor, width: 2),
                color: AppColors.bgSecondary,
              ),
              child: ClipOval(
                child:
                    card.profile.primaryPhotoUrl != null &&
                            card.profile.primaryPhotoUrl!.isNotEmpty
                        ? Image.network(
                            card.profile.primaryPhotoUrl!,
                            fit: BoxFit.cover,
                          )
                        : Container(
                            alignment: Alignment.center,
                            child: Text(
                              card.profile.firstName.isEmpty
                                  ? '?'
                                  : card.profile.firstName.substring(0, 1),
                              style: AppTextStyles.h2,
                            ),
                          ),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          '${card.profile.firstName}, ${card.profile.age}',
                          style: AppTextStyles.h3,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 8,
                          vertical: 2,
                        ),
                        decoration: BoxDecoration(
                          color: intentColor.withValues(alpha: 0.18),
                          borderRadius: BorderRadius.circular(
                            AppSpacing.radiusFull,
                          ),
                        ),
                        child: Text(
                          card.profile.intent,
                          style: AppTextStyles.labelTiny.copyWith(
                            color: intentColor,
                          ),
                        ),
                      ),
                    ],
                  ),
                  if (topReason != null)
                    Padding(
                      padding: const EdgeInsets.only(top: 4),
                      child: Text(
                        topReason,
                        style: AppTextStyles.bodySmall,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  const SizedBox(height: 8),
                  Row(
                    children: [
                      _ActionChip(
                        icon: Icons.bolt,
                        label: 'Spark',
                        color: AppColors.postbookPrimary,
                        onTap: onSpark,
                      ),
                      const SizedBox(width: 8),
                      _ActionChip(
                        icon: Icons.bookmark_outline,
                        label: 'Stash',
                        color: AppColors.accentPurple,
                        onTap: onStash,
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ActionChip extends StatelessWidget {
  const _ActionChip({
    required this.icon,
    required this.label,
    required this.color,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final Color color;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
        decoration: BoxDecoration(
          color: color.withValues(alpha: 0.15),
          borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 14, color: color),
            const SizedBox(width: 4),
            Text(
              label,
              style: AppTextStyles.labelTiny.copyWith(color: color),
            ),
          ],
        ),
      ),
    );
  }
}

Color _intentColor(String intent) {
  switch (intent) {
    case 'casual':
      return AppColors.statusWarning;
    case 'serious':
      return AppColors.accentPurple;
    case 'marriage':
      return AppColors.postgramPrimary;
    default:
      return AppColors.textTertiary;
  }
}
