import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/features/pulse/widgets/profile_detail_sheet.dart';
import 'package:atpost_app/features/pulse/widgets/spark_target_picker.dart';
import 'package:atpost_app/providers/pulse_providers.dart';
import 'package:atpost_app/services/pulse_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Sprint 3 — Stash shelf.
///
/// A horizontal carousel of stashed candidates. Sits in the Pulse screen
/// header below the mode toggle. Hidden entirely when the stash is empty.
///
/// Each card shows: avatar, first name, intent chip, "Stashed Nd ago" stamp,
/// and a "Tune in" CTA that opens the profile detail sheet. Long-press opens
/// a context menu with "Remove from Stash". A small dot + tooltip surfaces
/// when the candidate has a reactivation signal (e.g., a new prompt).
///
/// Reduced motion: no scroll snapping, no shimmer; just a plain horizontal
/// list. There are no continuous animations to suppress.
class StashShelf extends ConsumerWidget {
  const StashShelf({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final stashAsync = ref.watch(stashProvider);
    return stashAsync.when(
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
      data: (cards) {
        if (cards.isEmpty) return const SizedBox.shrink();
        return _StashShelfBody(cards: cards);
      },
    );
  }
}

class _StashShelfBody extends ConsumerWidget {
  const _StashShelfBody({required this.cards});

  final List<PulseCard> cards;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Container(
      padding: const EdgeInsets.fromLTRB(0, 6, 0, 10),
      decoration: const BoxDecoration(
        border: Border(
          bottom: BorderSide(color: AppColors.borderSubtle),
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(18, 0, 18, 8),
            child: Row(
              children: [
                const Icon(
                  Icons.bookmark_outline,
                  color: AppColors.accentPurple,
                  size: 18,
                ),
                const SizedBox(width: 6),
                Text(
                  'Your Stash',
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.textSecondary,
                  ),
                ),
                const SizedBox(width: 6),
                Text(
                  '· ${cards.length}',
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.textTertiary,
                  ),
                ),
              ],
            ),
          ),
          SizedBox(
            height: 124,
            child: ListView.separated(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 18),
              itemCount: cards.length,
              separatorBuilder: (_, _) => const SizedBox(width: 10),
              itemBuilder: (context, i) {
                final card = cards[i];
                return _StashCard(card: card);
              },
            ),
          ),
        ],
      ),
    );
  }
}

class _StashCard extends ConsumerWidget {
  const _StashCard({required this.card});

  final PulseCard card;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final intentColor = _intentColor(card.profile.intent);
    // Reactivation signal — a synthesised flag for now. Server-side this is
    // surfaced via the backend's Pulse-card payload (S4 polish — for now we
    // treat any non-null `recentPostId` newer than the stash row as a hint).
    final hasSignal = card.echoes.recentPostId != null;

    return GestureDetector(
      onLongPress: () => _showContextMenu(context, ref),
      child: Container(
        width: 150,
        padding: const EdgeInsets.all(10),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          border: Border.all(color: AppColors.borderSubtle),
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Stack(
                  clipBehavior: Clip.none,
                  children: [
                    Container(
                      width: 38,
                      height: 38,
                      decoration: BoxDecoration(
                        shape: BoxShape.circle,
                        border: Border.all(color: intentColor, width: 2),
                        color: AppColors.bgSecondary,
                      ),
                      child: ClipOval(
                        child: card.profile.primaryPhotoUrl != null &&
                                card.profile.primaryPhotoUrl!.isNotEmpty
                            ? Image.network(
                                card.profile.primaryPhotoUrl!,
                                fit: BoxFit.cover,
                                errorBuilder: (_, _, _) => Container(
                                  alignment: Alignment.center,
                                  child: Text(
                                    _initial(card.profile.firstName),
                                    style: AppTextStyles.label,
                                  ),
                                ),
                              )
                            : Container(
                                alignment: Alignment.center,
                                child: Text(
                                  _initial(card.profile.firstName),
                                  style: AppTextStyles.label,
                                ),
                              ),
                      ),
                    ),
                    if (hasSignal)
                      Positioned(
                        top: -2,
                        right: -2,
                        child: Tooltip(
                          message: 'New prompt',
                          child: Container(
                            width: 10,
                            height: 10,
                            decoration: const BoxDecoration(
                              color: AppColors.statusWarning,
                              shape: BoxShape.circle,
                            ),
                          ),
                        ),
                      ),
                  ],
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        card.profile.firstName.isEmpty
                            ? 'Stashed'
                            : card.profile.firstName,
                        style: AppTextStyles.label,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 6,
                          vertical: 1,
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
                ),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              _stashedAgo(),
              style: AppTextStyles.labelTiny.copyWith(
                color: AppColors.textTertiary,
              ),
            ),
            const Spacer(),
            SizedBox(
              width: double.infinity,
              child: TextButton.icon(
                style: TextButton.styleFrom(
                  backgroundColor:
                      AppColors.accentPurple.withValues(alpha: 0.15),
                  foregroundColor: AppColors.accentPurple,
                  padding: const EdgeInsets.symmetric(
                    horizontal: 6,
                    vertical: 2,
                  ),
                  shape: RoundedRectangleBorder(
                    borderRadius:
                        BorderRadius.circular(AppSpacing.radiusFull),
                  ),
                ),
                onPressed: () => _openDetail(context, ref),
                icon: const Icon(Icons.tune, size: 14),
                label: Text(
                  'Tune in',
                  style: AppTextStyles.labelTiny.copyWith(
                    color: AppColors.accentPurple,
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  String _initial(String name) =>
      name.isEmpty ? '?' : name.substring(0, 1).toUpperCase();

  String _stashedAgo() {
    // The backend doesn't yet surface `stashed_at` on the card payload. For
    // now we render a neutral label; S4 will swap to a real "Nd ago".
    return 'Stashed recently';
  }

  void _openDetail(BuildContext context, WidgetRef ref) {
    showPulseProfileDetailSheet(
      context: context,
      card: card,
      onSpark: () {
        // Re-open via the picker — it'll handle ref invalidation.
        SparkTargetPicker.show(context, candidate: card);
      },
      onStash: () async {
        try {
          await ref
              .read(pulseRepositoryProvider)
              .removeStash(card.profile.userId);
          ref.read(pulseTelemetryProvider).stashRemoved();
          ref.invalidate(stashProvider);
          if (!context.mounted) return;
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Removed from Stash.')),
          );
        } catch (_) {
          if (!context.mounted) return;
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Could not remove from Stash.')),
          );
        }
      },
      onPass: () {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Passed from Stash.')),
        );
      },
    );
  }

  Future<void> _showContextMenu(BuildContext context, WidgetRef ref) async {
    final action = await showModalBottomSheet<String>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      builder: (ctx) => SafeArea(
        child: Wrap(
          children: [
            ListTile(
              leading: const Icon(
                Icons.delete_outline,
                color: AppColors.statusError,
              ),
              title: Text(
                'Remove from Stash',
                style: AppTextStyles.body.copyWith(
                  color: AppColors.statusError,
                ),
              ),
              onTap: () => Navigator.of(ctx).pop('remove'),
            ),
            ListTile(
              leading: const Icon(Icons.close),
              title: Text('Cancel', style: AppTextStyles.body),
              onTap: () => Navigator.of(ctx).pop(),
            ),
          ],
        ),
      ),
    );
    if (action == 'remove') {
      try {
        await ref
            .read(pulseRepositoryProvider)
            .removeStash(card.profile.userId);
        ref.read(pulseTelemetryProvider).stashRemoved();
        ref.invalidate(stashProvider);
        if (!context.mounted) return;
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Removed from Stash.')),
        );
      } catch (_) {
        if (!context.mounted) return;
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Could not remove from Stash.')),
        );
      }
    }
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
