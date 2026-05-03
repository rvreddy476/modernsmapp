// Contextual paywall — Extend match.
//
// Opened when a free user taps the "Extend" button on an expiring match.
// Match-extend is a Premium-only feature; this paywall routes to
// /pulse/premium with `paywall:match_extend` as the attribution source.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

class MatchExtendPaywall {
  MatchExtendPaywall._();

  static Future<void> show(BuildContext context) {
    return showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgPrimary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
      ),
      builder: (_) => const _MatchExtendPaywallBody(),
    );
  }
}

class _MatchExtendPaywallBody extends StatelessWidget {
  const _MatchExtendPaywallBody();

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.fromLTRB(
        20,
        12,
        20,
        20 + MediaQuery.viewInsetsOf(context).bottom,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Center(
            child: Container(
              width: 36,
              height: 4,
              decoration: BoxDecoration(
                color: AppColors.borderSubtle,
                borderRadius: BorderRadius.circular(999),
              ),
            ),
          ),
          const SizedBox(height: 16),
          const Icon(
            Icons.history_toggle_off_rounded,
            size: 40,
            color: AppColors.postbookPrimary,
          ),
          const SizedBox(height: 12),
          Semantics(
            header: true,
            child: Text(
              'Extend this match',
              style: AppTextStyles.h2,
              textAlign: TextAlign.center,
            ),
          ),
          const SizedBox(height: 8),
          Text(
            'Premium adds 7 days to a match that\'s about to expire. Use it '
            'when a conversation is finally finding its rhythm and you don\'t '
            'want the timer to cut it short.',
            style: AppTextStyles.body,
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 20),
          ElevatedButton(
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            onPressed: () {
              Navigator.of(context).pop();
              context.push('/pulse/premium');
            },
            child: const Text('See Pulse Premium'),
          ),
          const SizedBox(height: 8),
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text(
              'Maybe later',
              style: TextStyle(color: AppColors.textTertiary),
            ),
          ),
        ],
      ),
    );
  }
}
