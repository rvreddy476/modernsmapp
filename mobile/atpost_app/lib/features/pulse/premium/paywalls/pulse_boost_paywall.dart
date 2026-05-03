// Contextual paywall — Pulse Boost.
//
// Opened when a free user taps the Boost CTA. Two paths:
//   1. Buy a single Boost token (`boost_49` plan, ₹49 one-shot).
//   2. Subscribe to Premium for daily Boost.
//
// The single-Boost path runs the same checkout flow with `plan_id = boost_49`
// and the source `paywall:boost`.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/pulse/premium/checkout_flow.dart';
import 'package:atpost_app/providers/pulse_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PulseBoostPaywall {
  PulseBoostPaywall._();

  static Future<void> show(BuildContext context) {
    return showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgPrimary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
      ),
      builder: (_) => const _PulseBoostPaywallBody(),
    );
  }
}

class _PulseBoostPaywallBody extends ConsumerWidget {
  const _PulseBoostPaywallBody();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final phase = ref.watch(checkoutFlowProvider).phase;
    final loading = phase == CheckoutPhase.creatingOrder ||
        phase == CheckoutPhase.verifying ||
        phase == CheckoutPhase.awaitingPayment;

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
            Icons.flash_on_rounded,
            size: 40,
            color: AppColors.postbookPrimary,
          ),
          const SizedBox(height: 12),
          Semantics(
            header: true,
            child: Text(
              'Pulse Boost',
              style: AppTextStyles.h2,
              textAlign: TextAlign.center,
            ),
          ),
          const SizedBox(height: 8),
          Text(
            'Surface +5 fresh candidates today. Premium gets a daily Boost; '
            'a single ₹49 Boost works without a subscription.',
            style: AppTextStyles.body,
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 20),
          ElevatedButton(
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            onPressed: loading
                ? null
                : () async {
                    Navigator.of(context).pop();
                    if (!context.mounted) return;
                    await ref
                        .read(checkoutFlowProvider.notifier)
                        .begin(
                          context: context,
                          planId: 'boost_49',
                          source: 'paywall:boost',
                        );
                  },
            child: loading
                ? const SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: Colors.white,
                    ),
                  )
                : const Text('Buy single Boost (₹49)'),
          ),
          const SizedBox(height: 8),
          OutlinedButton(
            style: OutlinedButton.styleFrom(
              padding: const EdgeInsets.symmetric(vertical: 14),
              side: const BorderSide(color: AppColors.postbookPrimary),
            ),
            onPressed: () {
              Navigator.of(context).pop();
              context.push('/pulse/premium');
            },
            child: const Text(
              'See Pulse Premium',
              style: TextStyle(color: AppColors.postbookPrimary),
            ),
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
