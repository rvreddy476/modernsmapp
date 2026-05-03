// Contextual paywall — Incognito browse.
//
// Opened when a free user toggles the Incognito switch in Safety Center.
// Shows the feature, why it helps, and routes to /pulse/premium.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

class IncognitoPaywall {
  IncognitoPaywall._();

  static Future<void> show(BuildContext context) {
    return showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgPrimary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
      ),
      builder: (_) => const _IncognitoPaywallBody(),
    );
  }
}

class _IncognitoPaywallBody extends StatelessWidget {
  const _IncognitoPaywallBody();

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
            Icons.visibility_off_rounded,
            size: 40,
            color: AppColors.postbookPrimary,
          ),
          const SizedBox(height: 12),
          Semantics(
            header: true,
            child: Text(
              'Browse incognito',
              style: AppTextStyles.h2,
              textAlign: TextAlign.center,
            ),
          ),
          const SizedBox(height: 8),
          Text(
            'Turn this on and your name won\'t appear in anyone\'s '
            'who-viewed list. Browse Pulse without leaving a trace until '
            'you\'re ready to Spark.',
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
