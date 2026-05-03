// Onboarding step 6 — pick a subscription plan.
//
// Loads plans from `subscriptionPlansProvider`. Highlights the Trial as
// "Free for 7 days, 10 leads". Tapping a card selects it; "Continue"
// advances to the payment step.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class OnboardingPlanStep extends ConsumerWidget {
  const OnboardingPlanStep({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final st = ref.watch(partnerOnboardingNotifier);
    final notifier = ref.read(partnerOnboardingNotifier.notifier);
    final plans = ref.watch(subscriptionPlansProvider);

    return Column(
      children: [
        Expanded(
          child: plans.when(
            loading: () => const Center(child: CircularProgressIndicator()),
            error: (_, _) => _ErrorState(
              onRetry: () => ref.invalidate(subscriptionPlansProvider),
            ),
            data: (list) => ListView(
              padding: const EdgeInsets.all(16),
              children: [
                Text('Choose your plan', style: AppTextStyles.h2),
                const SizedBox(height: 4),
                Text(
                  'You can switch any time from your partner profile.',
                  style: AppTextStyles.bodySmall,
                ),
                const SizedBox(height: 16),
                for (final p in list)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 10),
                    child: _PlanCard(
                      plan: p,
                      selected: st.selectedPlan?.id == p.id,
                      onTap: () => notifier.selectPlan(p),
                    ),
                  ),
              ],
            ),
          ),
        ),
        _ContinueBar(
          enabled: st.selectedPlan != null,
          onTap: () => notifier.next(),
        ),
      ],
    );
  }
}

class _PlanCard extends StatelessWidget {
  const _PlanCard({
    required this.plan,
    required this.selected,
    required this.onTap,
  });

  final SubscriptionPlan plan;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final isTrial = plan.isTrial;
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: selected
              ? AppColors.postbookPrimary.withValues(alpha: 0.12)
              : AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(
            color: selected
                ? AppColors.postbookPrimary
                : (isTrial ? AppColors.statusSuccess : AppColors.borderSubtle),
            width: selected ? 1.5 : 1,
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Text(plan.name, style: AppTextStyles.h2),
                const SizedBox(width: 8),
                if (isTrial)
                  Container(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 8,
                      vertical: 2,
                    ),
                    decoration: BoxDecoration(
                      color: AppColors.statusSuccess.withValues(alpha: 0.18),
                      borderRadius: BorderRadius.circular(99),
                    ),
                    child: Text(
                      'Free 7 days, 10 leads',
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.statusSuccess,
                      ),
                    ),
                  ),
                const Spacer(),
                Text(
                  isTrial ? 'FREE' : formatRupees(plan.priceInrPaise),
                  style: AppTextStyles.h2.copyWith(
                    color: AppColors.postbookPrimary,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Wrap(
              spacing: 12,
              runSpacing: 6,
              children: [
                _Stat(
                  icon: Icons.calendar_today,
                  label: '${plan.durationDays} days',
                ),
                _Stat(
                  icon: Icons.local_offer,
                  label: plan.isUnlimited
                      ? 'Unlimited leads'
                      : '${plan.leadAllotment} leads',
                ),
                _Stat(
                  icon: Icons.bolt,
                  label: 'Priority ${plan.planPriorityWeight.toStringAsFixed(1)}x',
                ),
                if (plan.isFairUse)
                  const _Stat(icon: Icons.balance, label: 'Fair use'),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _Stat extends StatelessWidget {
  const _Stat({required this.icon, required this.label});

  final IconData icon;
  final String label;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 14, color: AppColors.textTertiary),
        const SizedBox(width: 4),
        Text(label, style: AppTextStyles.bodySmall),
      ],
    );
  }
}

class _ErrorState extends StatelessWidget {
  const _ErrorState({required this.onRetry});

  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text('Could not load plans.', style: AppTextStyles.h3),
          const SizedBox(height: 8),
          OutlinedButton(onPressed: onRetry, child: const Text('Retry')),
        ],
      ),
    );
  }
}

class _ContinueBar extends StatelessWidget {
  const _ContinueBar({required this.enabled, required this.onTap});

  final bool enabled;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
      decoration: const BoxDecoration(
        color: AppColors.bgPrimary,
        border: Border(
          top: BorderSide(color: AppColors.borderSubtle, width: 0.5),
        ),
      ),
      child: SizedBox(
        width: double.infinity,
        height: 50,
        child: ElevatedButton(
          style: ElevatedButton.styleFrom(
            backgroundColor:
                enabled ? AppColors.postbookPrimary : AppColors.bgTertiary,
            foregroundColor: Colors.white,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            ),
          ),
          onPressed: enabled ? onTap : null,
          child: Text(
            'Continue',
            style: AppTextStyles.h3.copyWith(color: Colors.white),
          ),
        ),
      ),
    );
  }
}
