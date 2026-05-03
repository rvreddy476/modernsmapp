import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/providers/pulse_providers.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Sprint 3 — Match context banner overlay.
///
/// Rendered at the top of the Pulse chat surface. Shows:
///   - "You both Sparked [target_kind]: [target_ref summary]" — tap fills
///     the message input with a starter opener (via the supplied callback).
///   - A match-expiry pill: "Expires in 18 hours". Soft amber when < 24h.
///   - "Extend match (Premium)" CTA, only when expiry < 24h AND viewer is
///     the match author.
///
/// Spec §6.8.
class MatchContextBanner extends ConsumerWidget {
  const MatchContextBanner({
    super.key,
    required this.matchId,
    required this.onSuggestOpener,
  });

  final String matchId;

  /// Called when the viewer taps the spark-context card. The banner passes
  /// up a candidate opener string ("That [photo] caught my eye too…") so the
  /// chat screen can prefill the input.
  final ValueChanged<String> onSuggestOpener;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final matchAsync = ref.watch(pulseMatchProvider(matchId));
    return matchAsync.when(
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
      data: (match) {
        if (match == null) return const SizedBox.shrink();
        return _BannerBody(
          match: match,
          onSuggestOpener: onSuggestOpener,
        );
      },
    );
  }
}

class _BannerBody extends ConsumerWidget {
  const _BannerBody({
    required this.match,
    required this.onSuggestOpener,
  });

  final MatchDetail match;
  final ValueChanged<String> onSuggestOpener;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final ttl = match.timeUntilExpiry;
    final urgent = ttl != null && ttl.inHours < 24 && ttl.inSeconds > 0;

    final user = ref.watch(currentUserProvider);
    final viewerId = user.maybeWhen(
      data: (u) => u.id,
      orElse: () => '',
    );
    final canExtend = urgent &&
        match.authorUserId != null &&
        match.authorUserId == viewerId;

    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        border: const Border(
          bottom: BorderSide(color: AppColors.borderSubtle),
        ),
      ),
      padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (match.sparkContext != null)
            _SparkContextRow(
              context: match.sparkContext!,
              onTap: () => onSuggestOpener(
                _opener(match.sparkContext!),
              ),
            ),
          if (match.sparkContext != null && ttl != null)
            const SizedBox(height: 10),
          if (ttl != null)
            _ExpiryPill(ttl: ttl, urgent: urgent),
          if (canExtend) ...[
            const SizedBox(height: 8),
            _ExtendCta(matchId: match.id),
          ],
        ],
      ),
    );
  }
}

class _SparkContextRow extends StatelessWidget {
  const _SparkContextRow({required this.context, required this.onTap});

  final SparkContext context;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext ctx) {
    final summary = context.summary.isNotEmpty
        ? context.summary
        : context.targetRef;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      child: Container(
        padding: const EdgeInsets.all(10),
        decoration: BoxDecoration(
          color: AppColors.postbookPrimary.withValues(alpha: 0.10),
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          border: Border.all(
            color: AppColors.postbookPrimary.withValues(alpha: 0.35),
          ),
        ),
        child: Row(
          children: [
            const Icon(
              Icons.bolt,
              color: AppColors.postbookPrimary,
              size: 18,
            ),
            const SizedBox(width: 10),
            Expanded(
              child: RichText(
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                text: TextSpan(
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textSecondary,
                  ),
                  children: [
                    const TextSpan(text: 'You both Sparked the '),
                    TextSpan(
                      text: '${_kindLabel(context.targetKind)}: ',
                      style: AppTextStyles.label.copyWith(
                        color: AppColors.textPrimary,
                      ),
                    ),
                    TextSpan(
                      text: summary,
                      style: AppTextStyles.bodySmall.copyWith(
                        color: AppColors.textPrimary,
                      ),
                    ),
                  ],
                ),
              ),
            ),
            const SizedBox(width: 6),
            const Icon(
              Icons.edit_outlined,
              color: AppColors.postbookPrimary,
              size: 16,
            ),
          ],
        ),
      ),
    );
  }
}

String _kindLabel(SparkTargetKind kind) {
  switch (kind) {
    case SparkTargetKind.photo:
      return 'photo';
    case SparkTargetKind.prompt:
      return 'prompt';
    case SparkTargetKind.tuneAxis:
      return 'tune';
    case SparkTargetKind.echoQa:
      return 'Q&A';
    case SparkTargetKind.echoReel:
      return 'reel';
    case SparkTargetKind.echoCommunity:
      return 'community';
    case SparkTargetKind.echoPost:
      return 'post';
  }
}

String _opener(SparkContext ctx) {
  final summary = ctx.summary.isNotEmpty ? ctx.summary : ctx.targetRef;
  switch (ctx.targetKind) {
    case SparkTargetKind.photo:
      return 'That photo caught my eye — what was the moment?';
    case SparkTargetKind.prompt:
      return 'Loved your answer to "$summary". Tell me more?';
    case SparkTargetKind.tuneAxis:
      return 'Same wavelength on $summary — how did you land there?';
    case SparkTargetKind.echoQa:
      return 'Your Q&A answer made me pause. Curious how you got there.';
    case SparkTargetKind.echoReel:
      return 'That reel was perfect. What inspired it?';
    case SparkTargetKind.echoCommunity:
      return 'Fellow $summary person — what brought you in?';
    case SparkTargetKind.echoPost:
      return 'Your recent post stuck with me. Want to talk about it?';
  }
}

class _ExpiryPill extends StatelessWidget {
  const _ExpiryPill({required this.ttl, required this.urgent});

  final Duration ttl;
  final bool urgent;

  @override
  Widget build(BuildContext context) {
    final color = urgent ? AppColors.statusWarning : AppColors.textTertiary;
    final label = _formatTtl(ttl);
    return Row(
      children: [
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
          decoration: BoxDecoration(
            color: color.withValues(alpha: 0.18),
            borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
            border: Border.all(color: color.withValues(alpha: 0.45)),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(Icons.schedule, color: color, size: 14),
              const SizedBox(width: 6),
              Text(
                label,
                style: AppTextStyles.labelSmall.copyWith(color: color),
              ),
            ],
          ),
        ),
      ],
    );
  }
}

String _formatTtl(Duration d) {
  if (d.isNegative || d.inSeconds <= 0) return 'Expired';
  if (d.inDays >= 1) {
    final days = d.inDays;
    return 'Expires in $days day${days == 1 ? '' : 's'}';
  }
  if (d.inHours >= 1) {
    final hours = d.inHours;
    return 'Expires in $hours hour${hours == 1 ? '' : 's'}';
  }
  final minutes = d.inMinutes;
  return 'Expires in $minutes min${minutes == 1 ? '' : 's'}';
}

class _ExtendCta extends ConsumerStatefulWidget {
  const _ExtendCta({required this.matchId});

  final String matchId;

  @override
  ConsumerState<_ExtendCta> createState() => _ExtendCtaState();
}

class _ExtendCtaState extends ConsumerState<_ExtendCta> {
  bool _busy = false;

  Future<void> _extend() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await ref.read(pulseRepositoryProvider).extendMatch(widget.matchId);
      ref.invalidate(pulseMatchProvider(widget.matchId));
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Match extended.')),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Extend is a Premium feature. Upgrade to keep going.'),
        ),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Align(
      alignment: Alignment.centerLeft,
      child: TextButton.icon(
        onPressed: _busy ? null : _extend,
        style: TextButton.styleFrom(
          foregroundColor: AppColors.accentPurple,
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
        ),
        icon: _busy
            ? const SizedBox(
                width: 12,
                height: 12,
                child: CircularProgressIndicator(strokeWidth: 2),
              )
            : const Icon(Icons.workspace_premium, size: 16),
        label: const Text('Extend match (Premium)'),
      ),
    );
  }
}
