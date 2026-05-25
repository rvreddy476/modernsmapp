import 'dart:ui' as ui;

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/features/pulse/safety/report_block_sheet.dart';
import 'package:atpost_app/providers/pulse_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Sprint 2 — Pulse profile detail bottom sheet.
///
/// Snap points: peek (30%), half (60%), full (95%). Driven by a
/// `DraggableScrollableSheet`. Internally uses `showModalBottomSheet` so the
/// caller can `await` the result; we don't push a route.
///
/// Sections, in order (each collapsible):
///   1. Why this match  (`card.matchReasons`)
///   2. Tune            (`card.profile.tuneSummary`)
///   3. Echoes          (non-null fields of `card.echoes`)
///   4. Photos          (placeholder — primary only)
///   5. Prompts         (TODO S3)
///   6. Vouches         (TODO S4)
///   7. Safety footer   (Block / Report / Close — P1-4: real backend wired,
///                       see `_SafetyFooter` + `report_block_sheet.dart`)
///
/// Sticky bottom action bar: Stash • Spark • Pass.
Future<void> showPulseProfileDetailSheet({
  required BuildContext context,
  required PulseCard card,
  required VoidCallback onSpark,
  required VoidCallback onStash,
  required VoidCallback onPass,
  VoidCallback? onBlocked,
}) {
  return showModalBottomSheet<void>(
    context: context,
    backgroundColor: Colors.transparent,
    isScrollControlled: true,
    barrierColor: Colors.black.withValues(alpha: 0.55),
    builder: (ctx) => _ProfileDetailSheet(
      card: card,
      onSpark: () {
        Navigator.of(ctx).pop();
        onSpark();
      },
      onStash: () {
        Navigator.of(ctx).pop();
        onStash();
      },
      onPass: () {
        Navigator.of(ctx).pop();
        onPass();
      },
      onBlocked: () {
        // Close sheet first, then bubble up to caller so deck/match list
        // can refresh.
        if (Navigator.of(ctx).canPop()) Navigator.of(ctx).pop();
        onBlocked?.call();
      },
    ),
  );
}

class _ProfileDetailSheet extends StatelessWidget {
  const _ProfileDetailSheet({
    required this.card,
    required this.onSpark,
    required this.onStash,
    required this.onPass,
    required this.onBlocked,
  });

  final PulseCard card;
  final VoidCallback onSpark;
  final VoidCallback onStash;
  final VoidCallback onPass;
  final VoidCallback onBlocked;

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      initialChildSize: 0.6,
      minChildSize: 0.3,
      maxChildSize: 0.95,
      snap: true,
      snapSizes: const [0.3, 0.6, 0.95],
      builder: (context, controller) {
        return Stack(
          children: [
            ClipRRect(
              borderRadius: const BorderRadius.vertical(
                top: Radius.circular(28),
              ),
              child: Container(
                color: AppColors.bgSecondary,
                child: CustomScrollView(
                  controller: controller,
                  slivers: [
                    SliverToBoxAdapter(
                      child: _Header(card: card),
                    ),
                    SliverPadding(
                      padding: const EdgeInsets.fromLTRB(18, 8, 18, 110),
                      sliver: SliverList.list(
                        children: [
                          _Section(
                            title: 'Why this match',
                            child: _WhyMatch(reasons: card.matchReasons),
                          ),
                          _Section(
                            title: 'Tune',
                            child: _TuneCompare(
                              summary: card.profile.tuneSummary,
                            ),
                          ),
                          if (!card.echoes.isEmpty)
                            _Section(
                              title: 'Echoes',
                              child: _Echoes(echoes: card.echoes),
                            ),
                          _Section(
                            title: 'Photos',
                            child: _PhotosPlaceholder(card: card),
                          ),
                          _Section(
                            title: 'Prompts',
                            child: _Todo('Prompts coming in S3'),
                          ),
                          _Section(
                            title: 'Vouches',
                            child: _Todo('Vouches coming in S4'),
                          ),
                          const SizedBox(height: 12),
                          _SafetyFooter(
                            targetUserId: card.profile.userId,
                            targetName: card.profile.firstName,
                            onBlocked: onBlocked,
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ),
            // Sticky action bar.
            Positioned(
              left: 0,
              right: 0,
              bottom: 0,
              child: _StickyActions(
                onStash: onStash,
                onSpark: onSpark,
                onPass: onPass,
              ),
            ),
          ],
        );
      },
    );
  }
}

// ---------------------------------------------------------------------------
// Header — primary photo + overlay with avatar / name / age / distance.
// ---------------------------------------------------------------------------

class _Header extends StatelessWidget {
  const _Header({required this.card});

  final PulseCard card;

  @override
  Widget build(BuildContext context) {
    final photoUrl = card.profile.primaryPhotoUrl;
    final intentColor = _intentColor(card.profile.intent);

    return SizedBox(
      height: 280,
      child: Stack(
        fit: StackFit.expand,
        children: [
          if (photoUrl != null && photoUrl.isNotEmpty)
            Image.network(
              photoUrl,
              fit: BoxFit.cover,
              errorBuilder: (_, _, _) => const ColoredBox(
                color: AppColors.bgTertiary,
              ),
            )
          else
            const ColoredBox(color: AppColors.bgTertiary),
          if (card.profile.primaryPhotoBlurred)
            BackdropFilter(
              filter: ui.ImageFilter.blur(sigmaX: 18, sigmaY: 18),
              child: Container(
                color: AppColors.bgPrimary.withValues(alpha: 0.25),
              ),
            ),
          // Bottom gradient for legibility.
          Positioned(
            left: 0,
            right: 0,
            bottom: 0,
            height: 160,
            child: const DecoratedBox(
              decoration: BoxDecoration(
                gradient: LinearGradient(
                  begin: Alignment.topCenter,
                  end: Alignment.bottomCenter,
                  colors: [Color(0x0008080F), Color(0xCC08080F)],
                ),
              ),
            ),
          ),
          // Drag handle.
          Positioned(
            top: 8,
            left: 0,
            right: 0,
            child: Center(
              child: Container(
                width: 38,
                height: 4,
                decoration: BoxDecoration(
                  color: Colors.white24,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),
          ),
          Positioned(
            left: 18,
            right: 18,
            bottom: 16,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        '${card.profile.firstName}, ${card.profile.age}',
                        style: AppTextStyles.h1,
                      ),
                    ),
                    Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 10,
                        vertical: 4,
                      ),
                      decoration: BoxDecoration(
                        color: intentColor.withValues(alpha: 0.2),
                        borderRadius: BorderRadius.circular(
                          AppSpacing.radiusFull,
                        ),
                      ),
                      child: Text(
                        card.profile.intent,
                        style: AppTextStyles.labelSmall.copyWith(
                          color: intentColor,
                        ),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 4),
                Row(
                  children: [
                    if (card.profile.city != null)
                      Text(
                        card.profile.city!,
                        style: AppTextStyles.bodySmall.copyWith(
                          color: Colors.white70,
                        ),
                      ),
                    if (card.profile.distanceKm != null)
                      Padding(
                        padding: const EdgeInsets.only(left: 6),
                        child: Text(
                          '· ${card.profile.distanceKm} km',
                          style: AppTextStyles.bodySmall.copyWith(
                            color: Colors.white70,
                          ),
                        ),
                      ),
                  ],
                ),
                const SizedBox(height: 8),
                _TrustBadge(tier: card.profile.trustTier),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _TrustBadge extends StatelessWidget {
  const _TrustBadge({required this.tier});
  final String? tier;

  @override
  Widget build(BuildContext context) {
    if (tier == null || tier!.isEmpty) return const SizedBox.shrink();
    final label = switch (tier) {
      'aadhaar' => 'Aadhaar Verified',
      'phone' => 'Phone Verified',
      'email' => 'Email Verified',
      'vouched' => 'Vouched',
      _ => 'Verified',
    };
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        const Icon(Icons.verified, size: 14, color: AppColors.statusSuccess),
        const SizedBox(width: 4),
        Text(
          label,
          style: AppTextStyles.labelSmall.copyWith(
            color: AppColors.statusSuccess,
          ),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Section wrapper — collapsible.
// ---------------------------------------------------------------------------

class _Section extends StatefulWidget {
  const _Section({required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  State<_Section> createState() => _SectionState();
}

class _SectionState extends State<_Section> {
  bool _open = true;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 6),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        border: Border.all(color: AppColors.borderSubtle),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          InkWell(
            onTap: () => setState(() => _open = !_open),
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            child: Padding(
              padding: const EdgeInsets.symmetric(
                horizontal: 14,
                vertical: 12,
              ),
              child: Row(
                children: [
                  Expanded(
                    child: Text(widget.title, style: AppTextStyles.h3),
                  ),
                  Icon(
                    _open ? Icons.expand_less : Icons.expand_more,
                    color: AppColors.textTertiary,
                  ),
                ],
              ),
            ),
          ),
          if (_open)
            Padding(
              padding: const EdgeInsets.fromLTRB(14, 0, 14, 14),
              child: widget.child,
            ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Section bodies.
// ---------------------------------------------------------------------------

class _WhyMatch extends StatelessWidget {
  const _WhyMatch({required this.reasons});
  final List<MatchReason> reasons;

  @override
  Widget build(BuildContext context) {
    if (reasons.isEmpty) {
      return Text(
        'No reasons surfaced for this match yet.',
        style: AppTextStyles.bodySmall,
      );
    }
    return Column(
      children: [
        for (final r in reasons)
          Container(
            margin: const EdgeInsets.only(bottom: 8),
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: AppColors.bgSecondary,
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            ),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Icon(
                  _kindIcon(r.kind),
                  size: 18,
                  color: _kindColor(r.kind),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: Text(r.summary, style: AppTextStyles.bodySmall),
                ),
              ],
            ),
          ),
      ],
    );
  }
}

IconData _kindIcon(MatchReasonKind kind) {
  switch (kind) {
    case MatchReasonKind.tune:
      return Icons.tune;
    case MatchReasonKind.community:
      return Icons.groups;
    case MatchReasonKind.qaTopic:
      return Icons.question_answer;
    case MatchReasonKind.content:
      return Icons.article;
    case MatchReasonKind.recency:
      return Icons.access_time;
    case MatchReasonKind.geo:
      return Icons.place;
    case MatchReasonKind.trust:
      return Icons.verified_user;
    case MatchReasonKind.diversity:
      return Icons.diversity_3;
    case MatchReasonKind.unknown:
      return Icons.auto_awesome;
  }
}

Color _kindColor(MatchReasonKind kind) {
  switch (kind) {
    case MatchReasonKind.tune:
      return AppColors.accentPurple;
    case MatchReasonKind.community:
      return AppColors.posttubePrimary;
    case MatchReasonKind.qaTopic:
      return AppColors.postbookPrimary;
    case MatchReasonKind.content:
      return AppColors.postgramPrimary;
    case MatchReasonKind.geo:
      return AppColors.statusWarning;
    case MatchReasonKind.trust:
      return AppColors.statusSuccess;
    case MatchReasonKind.recency:
      return AppColors.posttubeSecondary;
    case MatchReasonKind.diversity:
      return AppColors.postgramSecondary;
    case MatchReasonKind.unknown:
      return AppColors.textTertiary;
  }
}

class _TuneCompare extends StatelessWidget {
  const _TuneCompare({required this.summary});
  final PulseTuneSummary? summary;

  @override
  Widget build(BuildContext context) {
    final s = summary;
    if (s == null) {
      return Text(
        'Tune not shared.',
        style: AppTextStyles.bodySmall,
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (s.lifestyleRhythm != null)
          _RhythmBar(value: s.lifestyleRhythm!.clamp(1, 5)),
        const SizedBox(height: 8),
        if (s.conversationStyle != null)
          Wrap(
            spacing: 6,
            children: [
              Container(
                padding: const EdgeInsets.symmetric(
                  horizontal: 10,
                  vertical: 4,
                ),
                decoration: BoxDecoration(
                  color: AppColors.accentPurple.withValues(alpha: 0.18),
                  borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
                ),
                child: Text(
                  s.conversationStyle!,
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.accentPurple,
                  ),
                ),
              ),
            ],
          ),
        if (s.languages.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Wrap(
              spacing: 6,
              runSpacing: 6,
              children: [
                for (final lang in s.languages)
                  Container(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 8,
                      vertical: 4,
                    ),
                    decoration: BoxDecoration(
                      color: AppColors.bgTertiary,
                      borderRadius: BorderRadius.circular(
                        AppSpacing.radiusSmall,
                      ),
                    ),
                    child: Text(
                      lang,
                      style: AppTextStyles.labelTiny,
                    ),
                  ),
              ],
            ),
          ),
      ],
    );
  }
}

class _RhythmBar extends StatelessWidget {
  const _RhythmBar({required this.value});
  final int value;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Text('Rhythm', style: AppTextStyles.labelSmall),
        const SizedBox(width: 10),
        for (var i = 1; i <= 5; i++)
          Padding(
            padding: const EdgeInsets.only(right: 4),
            child: Container(
              width: 22,
              height: 6,
              decoration: BoxDecoration(
                color: i <= value
                    ? AppColors.postbookPrimary
                    : AppColors.bgTertiary,
                borderRadius: BorderRadius.circular(3),
              ),
            ),
          ),
      ],
    );
  }
}

class _Echoes extends StatelessWidget {
  const _Echoes({required this.echoes});
  final PulseEchoes echoes;

  @override
  Widget build(BuildContext context) {
    final chips = <Widget>[];
    if (echoes.topQaAnswerId != null) {
      chips.add(_echoChip(
        context,
        label: 'Top Q&A answer',
        icon: Icons.question_answer,
      ));
    }
    if (echoes.topReelId != null) {
      chips.add(_echoChip(
        context,
        label: 'Top reel',
        icon: Icons.play_circle_outline,
      ));
    }
    if (echoes.topCommunity != null) {
      chips.add(_echoChip(
        context,
        label: echoes.topCommunity!,
        icon: Icons.groups,
      ));
    }
    if (echoes.recentPostId != null) {
      chips.add(_echoChip(
        context,
        label: 'Recent post',
        icon: Icons.article,
      ));
    }
    if (chips.isEmpty) {
      return Text(
        'No echoes yet.',
        style: AppTextStyles.bodySmall,
      );
    }
    return Wrap(spacing: 8, runSpacing: 8, children: chips);
  }

  Widget _echoChip(
    BuildContext context, {
    required String label,
    required IconData icon,
  }) {
    return InkWell(
      onTap: () {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Open Echo (S3)')),
        );
      },
      borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
        decoration: BoxDecoration(
          color: AppColors.posttubePrimary.withValues(alpha: 0.16),
          borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 14, color: AppColors.posttubePrimary),
            const SizedBox(width: 6),
            Text(
              label,
              style: AppTextStyles.labelSmall.copyWith(
                color: AppColors.posttubePrimary,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _PhotosPlaceholder extends StatelessWidget {
  const _PhotosPlaceholder({required this.card});
  final PulseCard card;

  @override
  Widget build(BuildContext context) {
    final url = card.profile.primaryPhotoUrl;
    if (url == null || url.isEmpty) {
      return _Todo('No photos yet.');
    }
    return ClipRRect(
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      child: AspectRatio(
        aspectRatio: 1,
        child: Image.network(url, fit: BoxFit.cover),
      ),
    );
  }
}

class _Todo extends StatelessWidget {
  const _Todo(this.text);
  final String text;
  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      ),
      child: Text(text, style: AppTextStyles.bodySmall),
    );
  }
}

/// Block / Report / Close action row at the bottom of the profile detail
/// sheet. P1-4 wiring — every visible safety control calls the real
/// dating-service endpoint and surfaces a result toast.
class _SafetyFooter extends ConsumerWidget {
  const _SafetyFooter({
    required this.targetUserId,
    required this.targetName,
    required this.onBlocked,
  });

  final String targetUserId;
  final String targetName;
  final VoidCallback onBlocked;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    Future<void> openReport() async {
      // The sheet itself is already open; nest the report sheet on top.
      await ReportSheet.show(
        context,
        targetUserId: targetUserId,
        targetName: targetName,
      );
    }

    Future<void> doBlock() async {
      // showBlockDialog handles confirm + API + toast. We invalidate
      // the deck/match/conversation providers so the blocked user
      // disappears from every surface.
      final blocked = await showBlockDialog(
        context,
        ref,
        targetUserId: targetUserId,
        targetName: targetName,
      );
      if (!blocked) return;
      // Refetch surfaces that show this user.
      try {
        ref.invalidate(pulseTodayProvider);
      } catch (_) {}
      onBlocked();
    }

    if (targetUserId.isEmpty) {
      // Safety: shouldn't happen, but if the candidate has no id we
      // can't call the backend — render a passive Close-only footer
      // instead of a non-functional block/report.
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 6),
        child: Row(
          children: [
            Expanded(
              child: TextButton.icon(
                onPressed: () => Navigator.of(context).maybePop(),
                icon: const Icon(Icons.close, size: 16),
                label: const Text('Close'),
                style: TextButton.styleFrom(
                  foregroundColor: AppColors.textTertiary,
                ),
              ),
            ),
          ],
        ),
      );
    }

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Row(
        children: [
          Expanded(
            child: TextButton.icon(
              onPressed: doBlock,
              icon: const Icon(Icons.block, size: 16),
              label: const Text('Block'),
              style: TextButton.styleFrom(
                foregroundColor: AppColors.statusError,
              ),
            ),
          ),
          Expanded(
            child: TextButton.icon(
              onPressed: openReport,
              icon: const Icon(Icons.flag_outlined, size: 16),
              label: const Text('Report'),
              style: TextButton.styleFrom(
                foregroundColor: AppColors.statusWarning,
              ),
            ),
          ),
          Expanded(
            child: TextButton.icon(
              onPressed: () => Navigator.of(context).maybePop(),
              icon: const Icon(Icons.close, size: 16),
              label: const Text('Close'),
              style: TextButton.styleFrom(
                foregroundColor: AppColors.textTertiary,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Sticky bottom action bar.
// ---------------------------------------------------------------------------

class _StickyActions extends StatelessWidget {
  const _StickyActions({
    required this.onStash,
    required this.onSpark,
    required this.onPass,
  });

  final VoidCallback onStash;
  final VoidCallback onSpark;
  final VoidCallback onPass;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgPrimary.withValues(alpha: 0.92),
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
      child: Row(
        children: [
          Expanded(
            child: _BarButton(
              icon: Icons.bookmark_outline,
              label: 'Stash',
              color: AppColors.accentPurple,
              onTap: onStash,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            flex: 2,
            child: _BarButton(
              icon: Icons.bolt,
              label: 'Spark',
              color: AppColors.postbookPrimary,
              onTap: onSpark,
              filled: true,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: _BarButton(
              icon: Icons.close,
              label: 'Pass',
              color: AppColors.textTertiary,
              onTap: onPass,
            ),
          ),
        ],
      ),
    );
  }
}

class _BarButton extends StatelessWidget {
  const _BarButton({
    required this.icon,
    required this.label,
    required this.color,
    required this.onTap,
    this.filled = false,
  });

  final IconData icon;
  final String label;
  final Color color;
  final VoidCallback onTap;
  final bool filled;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
      child: Container(
        height: 44,
        decoration: BoxDecoration(
          color: filled ? color : color.withValues(alpha: 0.15),
          borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
        ),
        alignment: Alignment.center,
        child: Row(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(icon, color: filled ? Colors.white : color, size: 16),
            const SizedBox(width: 6),
            Text(
              label,
              style: AppTextStyles.label.copyWith(
                color: filled ? Colors.white : color,
              ),
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
