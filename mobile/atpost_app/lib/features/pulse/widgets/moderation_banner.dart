import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';

/// Sprint 6 — chat-message moderation banner.
///
/// Renders the per-message UI state for messages that came back from the
/// backend with a `moderation: { layer, action_taken, confidence }` field.
///
///   - `shadow` → no UI change (the banner is `null`).
///   - `warn`   → soft amber banner under the message.
///   - `block`  → message body replaced with "Message held for review."
///                + a reveal-anyway button (premium-only).
///   - `held`   → same as `block`.
///
/// The widget is intentionally stateless — the parent owns the
/// "revealed" toggle so the parent's PulseMessage list is the single
/// source of truth.
class PulseModerationDecision {
  const PulseModerationDecision({
    required this.layer,
    required this.actionTaken,
    this.confidence,
    this.suggestion,
  });

  final int layer;
  final String actionTaken;
  final double? confidence;
  final String? suggestion;

  bool get isShadow => actionTaken == 'shadow';
  bool get isWarn => actionTaken == 'warn';
  bool get isBlock => actionTaken == 'block' || actionTaken == 'held';

  static PulseModerationDecision? tryParse(Map<String, dynamic>? raw) {
    if (raw == null) return null;
    final action = (raw['action_taken'] ?? '').toString();
    if (action.isEmpty) return null;
    final layer = raw['layer'];
    final conf = raw['confidence'];
    return PulseModerationDecision(
      layer: layer is int ? layer : int.tryParse('${layer ?? ''}') ?? 1,
      actionTaken: action,
      confidence: conf is num ? conf.toDouble() : null,
      suggestion: raw['suggestion'] is String
          ? raw['suggestion'] as String
          : null,
    );
  }
}

/// Soft amber warning banner used for `warn` decisions.
class ModerationWarnBanner extends StatelessWidget {
  const ModerationWarnBanner({super.key, this.suggestion});

  final String? suggestion;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(top: 6, bottom: 4),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: const Color(0x33FFB300), // amber, 20% alpha
        borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
        border: Border.all(color: const Color(0xFFB37400)),
      ),
      child: Row(
        children: [
          const Icon(Icons.warning_amber, color: Color(0xFFB37400), size: 18),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              suggestion ??
                  'This message contains patterns that look like a scam. '
                      'Stay cautious.',
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textPrimary,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// Held / blocked-message placeholder. The reveal-anyway action is
/// premium-only — pass `isPremium=true` to enable the button.
class ModerationHeldPlaceholder extends StatelessWidget {
  const ModerationHeldPlaceholder({
    super.key,
    required this.isPremium,
    required this.revealed,
    required this.onReveal,
    this.body,
  });

  /// True if the viewer has Pulse Premium.
  final bool isPremium;

  /// True when the parent has flipped the per-message reveal toggle on.
  final bool revealed;

  /// Called when the reveal-anyway button is tapped. The parent should
  /// flip its local state for this message.
  final VoidCallback onReveal;

  /// The original body (only rendered when `revealed=true`).
  final String? body;

  @override
  Widget build(BuildContext context) {
    if (revealed) {
      return Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: const Color(0xFFB37400)),
        ),
        child: Text(
          body ?? '',
          style: AppTextStyles.bodySmall.copyWith(
            color: AppColors.textPrimary,
          ),
        ),
      );
    }
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              const Icon(
                Icons.shield_outlined,
                size: 18,
                color: AppColors.textSecondary,
              ),
              const SizedBox(width: 8),
              Text(
                'Message held for review.',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textSecondary,
                  fontStyle: FontStyle.italic,
                ),
              ),
            ],
          ),
          if (isPremium) ...[
            const SizedBox(height: 6),
            TextButton(
              onPressed: onReveal,
              style: TextButton.styleFrom(
                padding: EdgeInsets.zero,
                minimumSize: const Size(0, 0),
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
              child: Text(
                'Reveal anyway',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.postbookPrimary,
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}
