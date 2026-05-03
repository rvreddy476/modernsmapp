// Pulse Premium screen — Sprint 5.
//
// Lists the live plans from `GET /v1/dating/premium/plans`, presents the
// feature checklist (spec §14), and opens the checkout flow on Continue.
//
// Plan ordering: server-provided. The "yearly" plan card gets a `bestValue`
// halo when the badge field is set; the "quarterly" card gets a "save 17%"
// chip. We don't hard-code strings keyed on plan_id — the badge text comes
// from the backend so marketing can A/B test without an app release.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/features/pulse/premium/checkout_flow.dart';
import 'package:atpost_app/features/pulse/premium/welcome_premium_sheet.dart';
import 'package:atpost_app/providers/pulse_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class PremiumScreen extends ConsumerStatefulWidget {
  const PremiumScreen({super.key, this.source = 'premium_screen'});

  /// Telemetry attribution for the checkout call.
  final String source;

  // Feature list rendered by `_Body` further down. Lifted to the
  // public widget class so the StatelessWidget below can reach it via
  // `PremiumScreen._featureList` — it can't reach a static on the
  // private `_PremiumScreenState`.
  static const List<String> _featureList = [
    'Unlimited Sparks',
    'See who Sparked you',
    '25 Stash slots',
    'Incognito browse',
    'Pulse Boost — +5 daily, 1 per day',
    'Match-extend (+7 days)',
    'Advanced Tune filters',
    'Safe-meet check-in',
    'Priority moderation review',
    'Read receipts (per match)',
  ];

  @override
  ConsumerState<PremiumScreen> createState() => _PremiumScreenState();
}

class _PremiumScreenState extends ConsumerState<PremiumScreen> {
  String? _selectedPlanId;

  @override
  Widget build(BuildContext context) {
    final plansAsync = ref.watch(premiumPlansProvider);
    final premium = ref.watch(premiumStateProvider);

    // Listen for checkout success → show welcome sheet, then refresh state.
    ref.listen<CheckoutState>(checkoutFlowProvider, (prev, next) async {
      if (prev?.phase != CheckoutPhase.succeeded &&
          next.phase == CheckoutPhase.succeeded) {
        await WelcomePremiumSheet.show(context);
        if (!mounted) return;
        ref.invalidate(premiumStateProvider);
        ref.read(checkoutFlowProvider.notifier).reset();
      } else if (prev?.phase != CheckoutPhase.failed &&
          next.phase == CheckoutPhase.failed) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              next.errorMessage ?? 'Checkout failed. Please try again.',
            ),
            action: SnackBarAction(
              label: 'Retry',
              onPressed: () {
                if (_selectedPlanId != null) {
                  _continueCheckout(_selectedPlanId!);
                }
              },
            ),
          ),
        );
      }
    });

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        title: Text('Pulse Premium', style: AppTextStyles.h2),
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        iconTheme: const IconThemeData(color: AppColors.textPrimary),
      ),
      body: plansAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(
            color: AppColors.postbookPrimary,
          ),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  'Could not load plans.',
                  style: AppTextStyles.body,
                  textAlign: TextAlign.center,
                ),
                const SizedBox(height: 12),
                ElevatedButton(
                  onPressed: () => ref.invalidate(premiumPlansProvider),
                  child: const Text('Retry'),
                ),
              ],
            ),
          ),
        ),
        data: (plans) => _Body(
          plans: plans.where((p) => !p.isOneShot).toList(),
          selectedPlanId: _selectedPlanId,
          onSelect: (id) => setState(() => _selectedPlanId = id),
          isPremium: premium.maybeWhen(
            data: (s) => s.active,
            orElse: () => false,
          ),
          onContinue: () {
            if (_selectedPlanId != null) {
              _continueCheckout(_selectedPlanId!);
            }
          },
          phase: ref.watch(checkoutFlowProvider).phase,
        ),
      ),
    );
  }

  Future<void> _continueCheckout(String planId) {
    return ref
        .read(checkoutFlowProvider.notifier)
        .begin(context: context, planId: planId, source: widget.source);
  }
}

class _Body extends StatelessWidget {
  const _Body({
    required this.plans,
    required this.selectedPlanId,
    required this.onSelect,
    required this.isPremium,
    required this.onContinue,
    required this.phase,
  });

  final List<PremiumPlan> plans;
  final String? selectedPlanId;
  final ValueChanged<String> onSelect;
  final bool isPremium;
  final VoidCallback onContinue;
  final CheckoutPhase phase;

  @override
  Widget build(BuildContext context) {
    final boldText = MediaQuery.boldTextOf(context);
    return SingleChildScrollView(
      padding: const EdgeInsets.fromLTRB(
        AppSpacing.xxl,
        AppSpacing.l,
        AppSpacing.xxl,
        AppSpacing.xxl,
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Semantics(
            header: true,
            child: Text(
              'Pulse Premium',
              style: AppTextStyles.h1.copyWith(
                fontWeight: boldText ? FontWeight.w900 : FontWeight.w700,
              ),
            ),
          ),
          const SizedBox(height: 4),
          Text(
            'More Pulse. More Sparks. More safety.',
            style: AppTextStyles.body.copyWith(
              color: AppColors.textSecondary,
            ),
          ),
          if (isPremium) ...[
            const SizedBox(height: AppSpacing.l),
            _ActiveBadge(),
          ],
          const SizedBox(height: AppSpacing.xxl),
          ...plans.map(
            (p) => Padding(
              padding: const EdgeInsets.only(bottom: 12),
              child: _PlanCard(
                plan: p,
                selected: p.id == selectedPlanId,
                onTap: () => onSelect(p.id),
              ),
            ),
          ),
          const SizedBox(height: AppSpacing.xxl),
          Text(
            'What you get',
            style: AppTextStyles.h3,
          ),
          const SizedBox(height: AppSpacing.m),
          ...PremiumScreen._featureList.map(
            (f) => Padding(
              padding: const EdgeInsets.symmetric(vertical: 6),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Icon(
                    Icons.check_rounded,
                    color: AppColors.postbookPrimary,
                    size: 20,
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Text(f, style: AppTextStyles.body),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: AppSpacing.xxl),
          SizedBox(
            height: 52,
            child: ElevatedButton(
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(14),
                ),
              ),
              onPressed: (selectedPlanId == null ||
                      isPremium ||
                      phase == CheckoutPhase.creatingOrder ||
                      phase == CheckoutPhase.awaitingPayment ||
                      phase == CheckoutPhase.verifying)
                  ? null
                  : onContinue,
              child: phase == CheckoutPhase.creatingOrder ||
                      phase == CheckoutPhase.verifying
                  ? const SizedBox(
                      height: 20,
                      width: 20,
                      child: CircularProgressIndicator(
                        strokeWidth: 2,
                        color: Colors.white,
                      ),
                    )
                  : Text(
                      isPremium ? 'You\'re a Premium member' : 'Continue',
                      style: AppTextStyles.body.copyWith(
                        color: Colors.white,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
            ),
          ),
          const SizedBox(height: AppSpacing.l),
          Text(
            'Auto-renews on the day before expiry via UPI. You can cancel '
            'anytime; access continues till the end of the period. Pricing in '
            'INR including GST.',
            style: AppTextStyles.labelSmall.copyWith(
              color: AppColors.textTertiary,
            ),
            textAlign: TextAlign.center,
          ),
        ],
      ),
    );
  }
}

class _PlanCard extends StatelessWidget {
  const _PlanCard({
    required this.plan,
    required this.selected,
    required this.onTap,
  });

  final PremiumPlan plan;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      button: true,
      selected: selected,
      label:
          '${plan.name}, ${plan.displayPriceInr}${plan.tagline.isNotEmpty ? ", ${plan.tagline}" : ""}',
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(16),
        child: Container(
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            color: selected
                ? AppColors.postbookPrimary.withValues(alpha: 0.10)
                : AppColors.bgSecondary,
            border: Border.all(
              color: selected
                  ? AppColors.postbookPrimary
                  : AppColors.borderSubtle,
              width: selected ? 2 : 1,
            ),
            borderRadius: BorderRadius.circular(16),
          ),
          child: Row(
            children: [
              Icon(
                selected
                    ? Icons.radio_button_checked
                    : Icons.radio_button_off,
                color: selected
                    ? AppColors.postbookPrimary
                    : AppColors.textTertiary,
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Expanded(
                          child: Text(
                            plan.name,
                            style: AppTextStyles.h3.copyWith(
                              fontWeight: FontWeight.w700,
                            ),
                          ),
                        ),
                        if (plan.badge != null && plan.badge!.isNotEmpty)
                          Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 8,
                              vertical: 4,
                            ),
                            decoration: BoxDecoration(
                              color: AppColors.postbookPrimary
                                  .withValues(alpha: 0.18),
                              borderRadius: BorderRadius.circular(999),
                            ),
                            child: Text(
                              plan.badge!,
                              style: AppTextStyles.labelSmall.copyWith(
                                color: AppColors.postbookPrimary,
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                          ),
                      ],
                    ),
                    const SizedBox(height: 4),
                    Text(
                      plan.tagline.isNotEmpty
                          ? plan.tagline
                          : '${plan.displayPriceInr} for ${plan.durationDays} days',
                      style: AppTextStyles.bodySmall.copyWith(
                        color: AppColors.textSecondary,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 8),
              Text(
                plan.displayPriceInr,
                style: AppTextStyles.h3.copyWith(
                  color: AppColors.textPrimary,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _ActiveBadge extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(
        horizontal: 12,
        vertical: 8,
      ),
      decoration: BoxDecoration(
        color: AppColors.postbookPrimary.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(
            Icons.verified_rounded,
            color: AppColors.postbookPrimary,
            size: 18,
          ),
          const SizedBox(width: 8),
          Text(
            'You\'re currently a Pulse Premium member.',
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.postbookPrimary,
              fontWeight: FontWeight.w600,
            ),
          ),
        ],
      ),
    );
  }
}
