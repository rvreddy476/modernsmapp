import 'package:atpost_app/features/monetization/widgets/tier_picker_sheet.dart';
import 'package:flutter/material.dart';

/// Inline paywall preview rendered in place of a post body when the
/// backend has redacted it (body_redacted = true). Tapping "Become a
/// member" opens the tier picker sheet.
class PaywallPreview extends StatelessWidget {
  final String creatorId;
  final String? creatorName;

  const PaywallPreview({
    super.key,
    required this.creatorId,
    this.creatorName,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: const Color(0xFFF5F0FF),
        borderRadius: BorderRadius.circular(14),
        border: Border.all(
          color: const Color(0xFFC4B5FD),
          width: 2,
          style: BorderStyle.solid,
        ),
      ),
      child: Column(
        children: [
          Container(
            width: 48,
            height: 48,
            decoration: const BoxDecoration(
              color: Color(0xFFE9DCFF),
              shape: BoxShape.circle,
            ),
            child: const Icon(
              Icons.lock_outline,
              color: Color(0xFF7C3AED),
              size: 24,
            ),
          ),
          const SizedBox(height: 10),
          Text(
            'Members-only content',
            style: theme.textTheme.titleMedium?.copyWith(
              fontWeight: FontWeight.w700,
              color: const Color(0xFF1F1B2E),
            ),
          ),
          const SizedBox(height: 4),
          Text(
            creatorName != null
                ? 'Become a member to unlock $creatorName\'s premium posts.'
                : 'Become a member to unlock premium posts from this creator.',
            textAlign: TextAlign.center,
            style: theme.textTheme.bodySmall?.copyWith(
              color: const Color(0xFF52466F),
            ),
          ),
          const SizedBox(height: 14),
          FilledButton(
            onPressed: () {
              TierPickerSheet.show(
                context,
                creatorId: creatorId,
                creatorName: creatorName,
              );
            },
            style: FilledButton.styleFrom(
              backgroundColor: const Color(0xFF7C3AED),
              foregroundColor: Colors.white,
            ),
            child: const Text('Become a member'),
          ),
        ],
      ),
    );
  }
}
