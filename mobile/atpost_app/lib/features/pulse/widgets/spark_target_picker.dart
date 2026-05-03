import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/features/pulse/widgets/match_celebration_sheet.dart';
import 'package:atpost_app/providers/pulse_providers.dart';
import 'package:atpost_app/services/pulse_crash_breadcrumbs.dart';
import 'package:atpost_app/services/pulse_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Sprint 3 — Spark target picker.
///
/// A bottom sheet opened when the viewer wants to Spark a candidate. The
/// candidate's profile is decomposed into "sparkable items" — photos, prompt
/// answers, Tune axes, Echoes — and the viewer picks one. That selection
/// becomes `(target_kind, target_ref)` on the POST /v1/dating/sparks call.
///
/// Spec reference: §6.4.
///
/// On success:
///   - if the response says `match_formed: true`, replace this sheet with
///     `MatchCelebrationSheet` (dramatic full-screen-ish modal);
///   - otherwise pop with a SnackBar "Spark sent."
///
/// Reduced motion: the only animation here is a subtle highlight on the
/// selected card. We honour `MediaQuery.disableAnimations` by clamping the
/// transition duration to zero in that case.
class SparkTargetPicker {
  SparkTargetPicker._();

  /// Opens the picker. Returns `true` if a spark was sent (mutual or not),
  /// `false` if the user cancelled. Always pops itself on success.
  static Future<bool> show(
    BuildContext context, {
    required PulseCard candidate,
  }) async {
    PulseBreadcrumbs.sparkPickerOpen(candidateId: candidate.candidateId);
    final result = await showModalBottomSheet<bool>(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      barrierColor: Colors.black.withValues(alpha: 0.55),
      builder: (ctx) => _SparkTargetPickerSheet(candidate: candidate),
    );
    final sent = result ?? false;
    PulseBreadcrumbs.sparkPickerClose(sent: sent);
    return sent;
  }
}

class _SparkTargetPickerSheet extends ConsumerStatefulWidget {
  const _SparkTargetPickerSheet({required this.candidate});

  final PulseCard candidate;

  @override
  ConsumerState<_SparkTargetPickerSheet> createState() =>
      _SparkTargetPickerSheetState();
}

class _SparkTargetPickerSheetState
    extends ConsumerState<_SparkTargetPickerSheet> {
  static const int _maxNoteLength = 120;

  SparkableItem? _selected;
  final TextEditingController _noteController = TextEditingController();
  bool _sending = false;
  String? _errorMessage;

  @override
  void dispose() {
    _noteController.dispose();
    super.dispose();
  }

  List<SparkableItem> _buildSparkableItems() {
    // The detail-sheet endpoint surface is still S4; for now we synthesise
    // sparkable items from what `PulseCard` already exposes. When the full
    // profile detail endpoint lands, this list will simply grow.
    final items = <SparkableItem>[];
    final p = widget.candidate.profile;
    final e = widget.candidate.echoes;

    // Primary photo (only one for now — full grid arrives with the profile
    // detail endpoint in S4).
    final photoUrl = p.primaryPhotoUrl;
    if (photoUrl != null && photoUrl.isNotEmpty) {
      items.add(
        SparkableItem(
          kind: SparkTargetKind.photo,
          ref: photoUrl,
          label: 'Their primary photo',
          imageUrl: photoUrl,
        ),
      );
    }

    // Tune axes — use the lightweight tune summary that the card carries.
    final tune = p.tuneSummary;
    if (tune != null) {
      if (tune.lifestyleRhythm != null) {
        items.add(
          SparkableItem(
            kind: SparkTargetKind.tuneAxis,
            ref: 'lifestyle_rhythm',
            label: 'Lifestyle rhythm',
            secondary: '${tune.lifestyleRhythm}/5',
          ),
        );
      }
      if (tune.conversationStyle != null) {
        items.add(
          SparkableItem(
            kind: SparkTargetKind.tuneAxis,
            ref: 'conversation_style',
            label: 'Conversation style',
            secondary: tune.conversationStyle,
          ),
        );
      }
      if (tune.faithFamilyWeight != null) {
        items.add(
          SparkableItem(
            kind: SparkTargetKind.tuneAxis,
            ref: 'faith_family_weight',
            label: 'Faith / family weight',
            secondary: '${tune.faithFamilyWeight}/5',
          ),
        );
      }
      for (final lang in tune.languages) {
        items.add(
          SparkableItem(
            kind: SparkTargetKind.tuneAxis,
            ref: 'language:$lang',
            label: 'Language',
            secondary: lang,
          ),
        );
      }
    }

    // Echoes.
    if (e.topQaAnswerId != null) {
      items.add(
        SparkableItem(
          kind: SparkTargetKind.echoQa,
          ref: e.topQaAnswerId!,
          label: 'Their top Q&A answer',
        ),
      );
    }
    if (e.topReelId != null) {
      items.add(
        SparkableItem(
          kind: SparkTargetKind.echoReel,
          ref: e.topReelId!,
          label: 'Their top reel',
        ),
      );
    }
    if (e.topCommunity != null) {
      items.add(
        SparkableItem(
          kind: SparkTargetKind.echoCommunity,
          ref: e.topCommunity!,
          label: 'Community',
          secondary: e.topCommunity,
        ),
      );
    }
    if (e.recentPostId != null) {
      items.add(
        SparkableItem(
          kind: SparkTargetKind.echoPost,
          ref: e.recentPostId!,
          label: 'A recent post',
        ),
      );
    }

    return items;
  }

  Future<void> _sendSpark() async {
    final selected = _selected;
    if (selected == null || _sending) return;

    setState(() {
      _sending = true;
      _errorMessage = null;
    });

    try {
      final repo = ref.read(pulseRepositoryProvider);
      final note = _noteController.text.trim();
      final result = await repo.createSpark(
        toUserId: widget.candidate.profile.userId,
        targetKind: selected.kind.toJsonValue(),
        targetRef: selected.ref,
        note: note.isEmpty ? null : note,
      );

      // Sprint 5: telemetry. target_kind only — no PII, no note text.
      ref.read(pulseTelemetryProvider).sparkCreated(
            targetKind: selected.kind.toJsonValue(),
          );

      // Refresh anything that might have changed: stash row may have been
      // promoted to a match, today might dedupe, matches list grew by one.
      ref.invalidate(stashProvider);
      ref.invalidate(pulseTodayProvider);
      ref.invalidate(pulseMatchesProvider);

      if (!mounted) return;

      if (result.matchFormed && result.match != null) {
        ref.read(pulseTelemetryProvider).matchFormed(
              matchId: result.match!.id,
              intent: result.match!.otherIntent ?? 'unknown',
            );
        // Capture the root navigator state BEFORE popping the picker. After
        // the pop, `context` no longer points at the picker route's element
        // tree, so we keep the navigator handle around to show the next
        // modal from the root.
        final rootNavigator = Navigator.of(context, rootNavigator: true);
        final rootContext = rootNavigator.context;
        Navigator.of(context).pop(true);
        WidgetsBinding.instance.addPostFrameCallback((_) {
          MatchCelebrationSheet.show(
            rootContext,
            match: result.match!,
            sparkContext: result.match!.sparkContext ??
                SparkContext(
                  targetKind: selected.kind,
                  targetRef: selected.ref,
                  summary: selected.label,
                ),
          );
        });
      } else {
        Navigator.of(context).pop(true);
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Spark sent.')),
        );
      }
    } catch (err) {
      if (!mounted) return;
      setState(() {
        _sending = false;
        _errorMessage = 'Could not send Spark. Try again.';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final items = _buildSparkableItems();
    final reducedMotion = MediaQuery.of(context).disableAnimations;

    return DraggableScrollableSheet(
      initialChildSize: 0.7,
      minChildSize: 0.4,
      maxChildSize: 0.95,
      snap: true,
      snapSizes: const [0.4, 0.7, 0.95],
      builder: (context, controller) {
        return ClipRRect(
          borderRadius: const BorderRadius.vertical(
            top: Radius.circular(28),
          ),
          child: Container(
            color: AppColors.bgSecondary,
            child: Stack(
              children: [
                CustomScrollView(
                  controller: controller,
                  slivers: [
                    SliverToBoxAdapter(
                      child: _PickerHeader(
                        candidate: widget.candidate,
                      ),
                    ),
                    if (items.isEmpty)
                      SliverFillRemaining(
                        hasScrollBody: false,
                        child: Padding(
                          padding: const EdgeInsets.all(24),
                          child: Center(
                            child: Text(
                              'Nothing to spark yet — this profile is still '
                              'sparse. Try again once they share more.',
                              style: AppTextStyles.bodySmall,
                              textAlign: TextAlign.center,
                            ),
                          ),
                        ),
                      )
                    else
                      SliverPadding(
                        padding: const EdgeInsets.fromLTRB(18, 8, 18, 16),
                        sliver: SliverList.list(
                          children: [
                            _PhotoGrid(
                              items: items
                                  .where(
                                    (it) => it.kind == SparkTargetKind.photo,
                                  )
                                  .toList(),
                              selected: _selected,
                              onSelect: (it) => setState(() => _selected = it),
                              reducedMotion: reducedMotion,
                            ),
                            ..._buildSection(
                              title: 'Tune',
                              items: items
                                  .where(
                                    (it) =>
                                        it.kind == SparkTargetKind.tuneAxis,
                                  )
                                  .toList(),
                              reducedMotion: reducedMotion,
                            ),
                            ..._buildSection(
                              title: 'Echoes',
                              items: items
                                  .where(
                                    (it) =>
                                        it.kind == SparkTargetKind.echoQa ||
                                        it.kind == SparkTargetKind.echoReel ||
                                        it.kind ==
                                            SparkTargetKind.echoCommunity ||
                                        it.kind == SparkTargetKind.echoPost,
                                  )
                                  .toList(),
                              reducedMotion: reducedMotion,
                            ),
                            if (_selected != null) ...[
                              const SizedBox(height: 8),
                              _NoteField(
                                controller: _noteController,
                                maxLength: _maxNoteLength,
                              ),
                            ],
                            if (_errorMessage != null) ...[
                              const SizedBox(height: 12),
                              Text(
                                _errorMessage!,
                                style: AppTextStyles.bodySmall.copyWith(
                                  color: AppColors.statusError,
                                ),
                              ),
                            ],
                            const SizedBox(height: 100),
                          ],
                        ),
                      ),
                  ],
                ),
                Positioned(
                  left: 0,
                  right: 0,
                  bottom: 0,
                  child: _SendBar(
                    enabled: _selected != null && !_sending,
                    sending: _sending,
                    onSend: _sendSpark,
                  ),
                ),
              ],
            ),
          ),
        );
      },
    );
  }

  List<Widget> _buildSection({
    required String title,
    required List<SparkableItem> items,
    required bool reducedMotion,
  }) {
    if (items.isEmpty) return const [];
    return [
      Padding(
        padding: const EdgeInsets.only(top: 18, bottom: 8),
        child: Text(title, style: AppTextStyles.h3),
      ),
      for (final item in items)
        Padding(
          padding: const EdgeInsets.only(bottom: 10),
          child: _ItemCard(
            item: item,
            selected: _selected == item,
            onTap: () {
              HapticFeedback.selectionClick();
              setState(() => _selected = item);
            },
            reducedMotion: reducedMotion,
          ),
        ),
    ];
  }
}

class _PickerHeader extends StatelessWidget {
  const _PickerHeader({required this.candidate});

  final PulseCard candidate;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 14, 18, 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Center(
            child: Container(
              width: 38,
              height: 4,
              decoration: BoxDecoration(
                color: AppColors.borderMedium,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          const SizedBox(height: 14),
          Text('What sparked your interest?', style: AppTextStyles.h2),
          const SizedBox(height: 4),
          Text(
            'Pick one specific thing about ${candidate.profile.firstName} '
            'that drew you in.',
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.textTertiary,
            ),
          ),
        ],
      ),
    );
  }
}

class _PhotoGrid extends StatelessWidget {
  const _PhotoGrid({
    required this.items,
    required this.selected,
    required this.onSelect,
    required this.reducedMotion,
  });

  final List<SparkableItem> items;
  final SparkableItem? selected;
  final ValueChanged<SparkableItem> onSelect;
  final bool reducedMotion;

  @override
  Widget build(BuildContext context) {
    if (items.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text('Photos', style: AppTextStyles.h3),
          ),
          GridView.builder(
            shrinkWrap: true,
            physics: const NeverScrollableScrollPhysics(),
            gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
              crossAxisCount: 2,
              crossAxisSpacing: 10,
              mainAxisSpacing: 10,
              childAspectRatio: 1,
            ),
            itemCount: items.length,
            itemBuilder: (context, i) {
              final item = items[i];
              final isSelected = selected == item;
              return GestureDetector(
                onTap: () {
                  HapticFeedback.selectionClick();
                  onSelect(item);
                },
                child: AnimatedContainer(
                  duration: reducedMotion
                      ? Duration.zero
                      : const Duration(milliseconds: 140),
                  decoration: BoxDecoration(
                    borderRadius: BorderRadius.circular(
                      AppSpacing.radiusMedium,
                    ),
                    border: Border.all(
                      color: isSelected
                          ? AppColors.postbookPrimary
                          : AppColors.borderSubtle,
                      width: isSelected ? 3 : 1,
                    ),
                  ),
                  child: ClipRRect(
                    borderRadius: BorderRadius.circular(
                      AppSpacing.radiusMedium - 2,
                    ),
                    child: Stack(
                      fit: StackFit.expand,
                      children: [
                        if (item.imageUrl != null && item.imageUrl!.isNotEmpty)
                          Image.network(
                            item.imageUrl!,
                            fit: BoxFit.cover,
                            errorBuilder: (_, _, _) =>
                                const ColoredBox(color: AppColors.bgTertiary),
                          )
                        else
                          const ColoredBox(color: AppColors.bgTertiary),
                        if (isSelected)
                          Container(
                            decoration: BoxDecoration(
                              color: AppColors.postbookPrimary
                                  .withValues(alpha: 0.20),
                            ),
                            alignment: Alignment.topRight,
                            padding: const EdgeInsets.all(6),
                            child: const Icon(
                              Icons.check_circle,
                              color: Colors.white,
                              size: 22,
                            ),
                          ),
                      ],
                    ),
                  ),
                ),
              );
            },
          ),
        ],
      ),
    );
  }
}

class _ItemCard extends StatelessWidget {
  const _ItemCard({
    required this.item,
    required this.selected,
    required this.onTap,
    required this.reducedMotion,
  });

  final SparkableItem item;
  final bool selected;
  final VoidCallback onTap;
  final bool reducedMotion;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      child: AnimatedContainer(
        duration: reducedMotion
            ? Duration.zero
            : const Duration(milliseconds: 140),
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: selected
              ? AppColors.postbookPrimary.withValues(alpha: 0.10)
              : AppColors.bgCard,
          border: Border.all(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
            width: selected ? 2 : 1,
          ),
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(
              _iconFor(item.kind),
              color: selected
                  ? AppColors.postbookPrimary
                  : AppColors.textTertiary,
              size: 20,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    item.label,
                    style: AppTextStyles.label.copyWith(
                      color: AppColors.textPrimary,
                    ),
                  ),
                  if (item.secondary != null && item.secondary!.isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.only(top: 4),
                      child: Text(
                        item.secondary!,
                        style: AppTextStyles.bodySmall.copyWith(
                          color: AppColors.textSecondary,
                        ),
                      ),
                    ),
                ],
              ),
            ),
            if (selected)
              const Icon(
                Icons.check_circle,
                color: AppColors.postbookPrimary,
                size: 22,
              ),
          ],
        ),
      ),
    );
  }
}

IconData _iconFor(SparkTargetKind kind) {
  switch (kind) {
    case SparkTargetKind.photo:
      return Icons.photo_outlined;
    case SparkTargetKind.prompt:
      return Icons.format_quote_outlined;
    case SparkTargetKind.tuneAxis:
      return Icons.tune;
    case SparkTargetKind.echoQa:
      return Icons.question_answer_outlined;
    case SparkTargetKind.echoReel:
      return Icons.play_circle_outline;
    case SparkTargetKind.echoCommunity:
      return Icons.groups_outlined;
    case SparkTargetKind.echoPost:
      return Icons.article_outlined;
  }
}

class _NoteField extends StatelessWidget {
  const _NoteField({required this.controller, required this.maxLength});

  final TextEditingController controller;
  final int maxLength;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: TextField(
        controller: controller,
        maxLength: maxLength,
        maxLines: 1,
        style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
        decoration: InputDecoration(
          hintText: 'Add a one-liner (optional)',
          hintStyle: AppTextStyles.bodySmall.copyWith(
            color: AppColors.textMuted,
          ),
          border: InputBorder.none,
          counterStyle: AppTextStyles.labelTiny,
        ),
      ),
    );
  }
}

class _SendBar extends StatelessWidget {
  const _SendBar({
    required this.enabled,
    required this.sending,
    required this.onSend,
  });

  final bool enabled;
  final bool sending;
  final VoidCallback onSend;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgPrimary.withValues(alpha: 0.94),
        border: const Border(
          top: BorderSide(color: AppColors.borderSubtle),
        ),
      ),
      padding: EdgeInsets.fromLTRB(
        16,
        12,
        16,
        12 + MediaQuery.of(context).padding.bottom,
      ),
      child: SizedBox(
        height: 48,
        child: ElevatedButton.icon(
          onPressed: enabled ? onSend : null,
          style: ElevatedButton.styleFrom(
            backgroundColor: AppColors.postbookPrimary,
            disabledBackgroundColor: AppColors.bgTertiary,
            disabledForegroundColor: AppColors.textDim,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
            ),
          ),
          icon: sending
              ? const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(
                    color: Colors.white,
                    strokeWidth: 2,
                  ),
                )
              : const Icon(Icons.bolt, color: Colors.white),
          label: Text(
            sending ? 'Sending...' : 'Send Spark',
            style: AppTextStyles.label.copyWith(color: Colors.white),
          ),
        ),
      ),
    );
  }
}
