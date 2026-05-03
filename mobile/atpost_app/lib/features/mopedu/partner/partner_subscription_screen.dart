// Partner subscription — Sprint 4 polish.
//
// Sprint 2 shipped the bones (current plan card, plan picker, payment
// flow). Sprint 4 enhances:
//   * Prominent status banner — Active (green), Grace (amber + countdown),
//     Expired (red).
//   * Lead usage progress bar with "Approaching limit" hint at >80%.
//   * Auto-renewal toggle card with sub-text + T&C confirmation modal.
//   * Renewal failure banner (`renewal_failure_count >= 1`).
//   * Renew now / Switch plan / Cancel auto-renew action row.
//   * Payment history list (verified/pending/rejected, plan, amount,
//     method, date).
//
// IDEMPOTENCY: every renewal or switch tap mints a fresh UUIDv4 in the
// `subscriptionRenewProvider`. Re-tries on the same intent reuse the key
// only if the caller threads one — we never reuse automatically.
//
// PRIVACY: telemetry never carries earnings amounts, partner phones, or
// document numbers. Plan ids are categorical and explicitly allowed.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:atpost_app/services/mopedu_crash_breadcrumbs.dart';
import 'package:atpost_app/services/upi_intent_helper.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PartnerSubscriptionScreen extends ConsumerWidget {
  const PartnerSubscriptionScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncSub = ref.watch(subscriptionDetailProvider);
    final asyncPlans = ref.watch(subscriptionPlansProvider);
    final asyncPayments = ref.watch(subscriptionPaymentsProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () => context.canPop() ? context.pop() : context.go('/'),
        ),
        title: Text('Subscription', style: AppTextStyles.h2),
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          ref.invalidate(subscriptionDetailProvider);
          ref.invalidate(mySubscriptionProvider);
          ref.invalidate(subscriptionPlansProvider);
          ref.invalidate(subscriptionPaymentsProvider);
          await ref.read(subscriptionDetailProvider.future);
        },
        child: ListView(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
          children: [
            asyncSub.when(
              loading: () => const Padding(
                padding: EdgeInsets.symmetric(vertical: 32),
                child: Center(child: CircularProgressIndicator()),
              ),
              error: (_, _) => Text(
                'Could not load subscription.',
                style: AppTextStyles.bodySmall,
              ),
              data: (sub) {
                if (sub == null) {
                  return const _NoSubscription();
                }
                return Column(
                  children: [
                    if (sub.renewalFailureCount >= 1)
                      _RenewalFailureBanner(count: sub.renewalFailureCount),
                    if (sub.renewalFailureCount >= 1) const SizedBox(height: 10),
                    _CurrentPlanCard(sub: sub),
                    const SizedBox(height: 12),
                    _LeadUsageCard(sub: sub),
                    const SizedBox(height: 12),
                    _AutoRenewCard(sub: sub),
                    const SizedBox(height: 12),
                    _ActionRow(
                      sub: sub,
                      onRenew: () => _onRenewCurrent(context, ref, sub),
                      onSwitch: () => _onSwitchPlan(context, ref, sub),
                    ),
                  ],
                );
              },
            ),
            const SizedBox(height: 20),
            Text('Switch plan', style: AppTextStyles.h3),
            const SizedBox(height: 8),
            asyncPlans.when(
              loading: () => const Center(child: CircularProgressIndicator()),
              error: (_, _) => Text(
                'Could not load plans.',
                style: AppTextStyles.bodySmall,
              ),
              data: (list) => Column(
                children: [
                  for (final p in list)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 10),
                      child: _SwitchPlanCard(
                        plan: p,
                        currentPlanId: asyncSub.value?.planId,
                        onSelect: () =>
                            _onPickPlan(context, ref, p, asyncSub.value),
                      ),
                    ),
                ],
              ),
            ),
            const SizedBox(height: 20),
            Text('Payment history', style: AppTextStyles.h3),
            const SizedBox(height: 8),
            asyncPayments.when(
              loading: () => const Padding(
                padding: EdgeInsets.symmetric(vertical: 16),
                child: Center(child: CircularProgressIndicator()),
              ),
              error: (_, _) => Text(
                'Could not load payment history.',
                style: AppTextStyles.bodySmall,
              ),
              data: (list) => _PaymentHistory(items: list, plans: asyncPlans.value ?? const []),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _onRenewCurrent(
    BuildContext context,
    WidgetRef ref,
    PartnerSubscription sub,
  ) async {
    final plans = ref.read(subscriptionPlansProvider).value ?? const [];
    final plan = plans.firstWhere(
      (p) => p.id == sub.planId,
      orElse: () => plans.isNotEmpty
          ? plans.first
          : const SubscriptionPlan(
              id: '',
              name: '',
              priceInrPaise: 0,
              durationDays: 30,
              leadAllotment: 0,
              planPriorityWeight: 1.0,
              isUnlimited: false,
              isFairUse: false,
              isActive: true,
            ),
    );
    if (plan.id.isEmpty) return;
    await _onPickPlan(context, ref, plan, sub);
  }

  Future<void> _onSwitchPlan(
    BuildContext context,
    WidgetRef ref,
    PartnerSubscription sub,
  ) async {
    // Just scrolls focus to the plan picker section below; the plan list
    // is already rendered. We surface a snackbar nudge.
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Pick a plan below to switch.')),
    );
  }

  Future<void> _onPickPlan(
    BuildContext context,
    WidgetRef ref,
    SubscriptionPlan plan,
    PartnerSubscription? currentSub,
  ) async {
    final method = await _pickMethod(context);
    if (method == null) return;
    MopeduBreadcrumbs.subscriptionRenewStart(planId: plan.id);
    final notifier = ref.read(subscriptionRenewProvider.notifier);
    final payment = await notifier.renew(
      planId: plan.id,
      paymentMethod: method,
      fromPlanId: currentSub?.planId,
    );
    if (!context.mounted) return;
    if (payment == null) {
      MopeduBreadcrumbs.subscriptionRenewFail(
        planId: plan.id,
        reason: 'create_order_failed',
      );
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not start payment. Please retry.')),
      );
      return;
    }
    if (method == 'upi' && (payment.upiIntentUrl?.isNotEmpty ?? false)) {
      await launchUPIIntent(context, payment.upiIntentUrl!);
    }
    MopeduBreadcrumbs.subscriptionRenewComplete(planId: plan.id);
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Subscription updated.')),
      );
    }
  }

  Future<String?> _pickMethod(BuildContext context) async {
    return showModalBottomSheet<String>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Pay with', style: AppTextStyles.h2),
              const SizedBox(height: 8),
              ListTile(
                leading: const Icon(Icons.account_balance_wallet),
                title: const Text('Wallet'),
                onTap: () => Navigator.of(context).pop('wallet'),
              ),
              ListTile(
                leading: const Icon(Icons.qr_code_2),
                title: const Text('UPI'),
                onTap: () => Navigator.of(context).pop('upi'),
              ),
              ListTile(
                leading: const Icon(Icons.upload_file),
                title: const Text('Manual proof'),
                onTap: () => Navigator.of(context).pop('manual_proof'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _NoSubscription extends StatelessWidget {
  const _NoSubscription();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.workspace_premium, color: AppColors.statusWarning),
          const SizedBox(height: 6),
          Text('No active subscription', style: AppTextStyles.h3),
          const SizedBox(height: 4),
          Text(
            'Pick a plan below to start receiving ride offers.',
            style: AppTextStyles.bodySmall,
          ),
        ],
      ),
    );
  }
}

class _RenewalFailureBanner extends StatelessWidget {
  const _RenewalFailureBanner({required this.count});
  final int count;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.statusWarning.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.statusWarning),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.error_outline, color: AppColors.statusWarning),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  count == 1
                      ? 'Last renewal failed'
                      : 'Renewal failed $count times',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.statusWarning,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  'Insufficient wallet balance. Top up your wallet to ensure '
                  'uninterrupted service.',
                  style: AppTextStyles.bodySmall,
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _CurrentPlanCard extends StatelessWidget {
  const _CurrentPlanCard({required this.sub});
  final PartnerSubscription sub;

  Color _statusColor() {
    switch (sub.status) {
      case SubscriptionStatus.active:
      case SubscriptionStatus.trial:
        return AppColors.statusSuccess;
      case SubscriptionStatus.gracePeriod:
        return AppColors.statusWarning;
      default:
        return AppColors.statusError;
    }
  }

  String _statusLine() {
    final exp = sub.expiresAt;
    final dt = '${exp.day}/${_monthName(exp.month)}/${exp.year}';
    switch (sub.status) {
      case SubscriptionStatus.active:
      case SubscriptionStatus.trial:
        return 'Active until $dt';
      case SubscriptionStatus.gracePeriod:
        final end = sub.graceEndsAt ?? sub.expiresAt;
        final daysLeft = end.difference(DateTime.now()).inDays;
        final clamped = daysLeft < 0 ? 0 : daysLeft;
        return 'Grace period ends in $clamped days. Renew to keep priority.';
      case SubscriptionStatus.expired:
        return 'Expired. Renew to start receiving leads again.';
      default:
        return sub.status.label;
    }
  }

  static String _monthName(int m) {
    const names = [
      'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
      'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
    ];
    if (m < 1 || m > 12) return '$m';
    return names[m - 1];
  }

  bool get _showCountdown =>
      sub.status == SubscriptionStatus.active ||
      sub.status == SubscriptionStatus.trial ||
      sub.status == SubscriptionStatus.gracePeriod;

  @override
  Widget build(BuildContext context) {
    final color = _statusColor();
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: color, width: 1.5),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(child: Text(sub.planName, style: AppTextStyles.h2)),
              Container(
                padding: const EdgeInsets.symmetric(
                  horizontal: 10,
                  vertical: 3,
                ),
                decoration: BoxDecoration(
                  color: color.withValues(alpha: 0.18),
                  borderRadius: BorderRadius.circular(99),
                ),
                child: Text(
                  sub.status.label,
                  style: AppTextStyles.labelSmall.copyWith(color: color),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            _statusLine(),
            style: AppTextStyles.body.copyWith(color: color),
          ),
          if (_showCountdown) ...[
            const SizedBox(height: 10),
            Row(
              children: [
                Expanded(
                  child: _StatBlock(
                    label: 'Days remaining',
                    value: '${sub.daysRemaining}',
                  ),
                ),
                Expanded(
                  child: _StatBlock(
                    label: 'Leads remaining',
                    value: '${sub.leadsRemaining}',
                  ),
                ),
              ],
            ),
          ],
        ],
      ),
    );
  }
}

class _LeadUsageCard extends StatelessWidget {
  const _LeadUsageCard({required this.sub});
  final PartnerSubscription sub;

  @override
  Widget build(BuildContext context) {
    final ratio = sub.leadUsageRatio;
    final approaching = ratio >= 0.8;
    final fillColor =
        approaching ? AppColors.statusWarning : AppColors.posttubePrimary;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  '${sub.leadsUsed} of ${sub.leadAllotment} leads used '
                  'this month',
                  style: AppTextStyles.label,
                ),
              ),
              Text(
                '${(ratio * 100).toStringAsFixed(0)}%',
                style: AppTextStyles.label.copyWith(color: fillColor),
              ),
            ],
          ),
          const SizedBox(height: 8),
          ClipRRect(
            borderRadius: BorderRadius.circular(99),
            child: LinearProgressIndicator(
              value: ratio,
              minHeight: 8,
              backgroundColor: AppColors.bgTertiary,
              valueColor: AlwaysStoppedAnimation<Color>(fillColor),
            ),
          ),
          if (approaching) ...[
            const SizedBox(height: 8),
            Row(
              children: [
                const Icon(
                  Icons.warning_amber_rounded,
                  size: 14,
                  color: AppColors.statusWarning,
                ),
                const SizedBox(width: 4),
                Text(
                  'Approaching limit — consider switching plan.',
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.statusWarning,
                  ),
                ),
              ],
            ),
          ],
        ],
      ),
    );
  }
}

class _AutoRenewCard extends ConsumerWidget {
  const _AutoRenewCard({required this.sub});
  final PartnerSubscription sub;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final mutation = ref.watch(autoRenewMutationProvider);
    final busy = mutation.isLoading;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(
                Icons.autorenew,
                color: AppColors.posttubePrimary,
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  'Auto-renew via wallet',
                  style: AppTextStyles.h3,
                ),
              ),
              Switch.adaptive(
                value: sub.autoRenew,
                onChanged: busy
                    ? null
                    : (v) => _confirmAndToggle(context, ref, sub, v),
              ),
            ],
          ),
          const SizedBox(height: 4),
          Text(
            "We'll deduct ${formatRupees(_priceFromContext(ref, sub))} from your "
            "wallet 12 hours before expiry. You'll get 3 retries on failure.",
            style: AppTextStyles.bodySmall,
          ),
        ],
      ),
    );
  }

  int _priceFromContext(WidgetRef ref, PartnerSubscription sub) {
    final plans = ref.read(subscriptionPlansProvider).value ?? const [];
    for (final p in plans) {
      if (p.id == sub.planId) return p.priceInrPaise;
    }
    return 0;
  }

  Future<void> _confirmAndToggle(
    BuildContext context,
    WidgetRef ref,
    PartnerSubscription sub,
    bool value,
  ) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text(
          value ? 'Turn on auto-renew?' : 'Turn off auto-renew?',
          style: AppTextStyles.h3,
        ),
        content: Text(
          value
              ? "We will charge your Mopedu wallet 12 hours before your plan "
                  "expires. If the wallet doesn't have enough balance we will "
                  "retry up to 3 times. You can cancel any time."
              : "We will not charge your wallet automatically. You will need "
                  "to renew manually before your plan expires to keep "
                  "receiving leads.",
          style: AppTextStyles.body,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              foregroundColor: Colors.white,
            ),
            onPressed: () => Navigator.of(context).pop(true),
            child: Text(value ? 'Turn on' : 'Turn off'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    if (!context.mounted) return;
    final ok = await ref
        .read(autoRenewMutationProvider.notifier)
        .setAutoRenew(value);
    if (!context.mounted) return;
    if (!ok) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text(
            'Could not update auto-renew preference. Please try again.',
          ),
        ),
      );
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            value ? 'Auto-renew turned on.' : 'Auto-renew turned off.',
          ),
        ),
      );
    }
  }
}

class _ActionRow extends StatelessWidget {
  const _ActionRow({
    required this.sub,
    required this.onRenew,
    required this.onSwitch,
  });

  // Kept on the widget so future variants (e.g. hide "Renew" when the plan
  // is unlimited) can branch on the current subscription without re-plumbing.
  // ignore: unused_field
  final PartnerSubscription sub;
  final VoidCallback onRenew;
  final VoidCallback onSwitch;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(
          child: ElevatedButton.icon(
            onPressed: onRenew,
            icon: const Icon(Icons.refresh, size: 16),
            label: const Text('Renew now'),
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              foregroundColor: Colors.white,
              padding: const EdgeInsets.symmetric(vertical: 12),
            ),
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: OutlinedButton.icon(
            onPressed: onSwitch,
            icon: const Icon(Icons.swap_horiz, size: 16),
            label: const Text('Switch plan'),
            style: OutlinedButton.styleFrom(
              padding: const EdgeInsets.symmetric(vertical: 12),
            ),
          ),
        ),
      ],
    );
  }
}

class _StatBlock extends StatelessWidget {
  const _StatBlock({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(value, style: AppTextStyles.h3),
        Text(label, style: AppTextStyles.labelSmall),
      ],
    );
  }
}

class _SwitchPlanCard extends StatelessWidget {
  const _SwitchPlanCard({
    required this.plan,
    required this.currentPlanId,
    required this.onSelect,
  });

  final SubscriptionPlan plan;
  final String? currentPlanId;
  final VoidCallback onSelect;

  @override
  Widget build(BuildContext context) {
    final isCurrent = plan.id == currentPlanId;
    return Opacity(
      opacity: isCurrent ? 0.55 : 1,
      child: InkWell(
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        onTap: isCurrent ? null : onSelect,
        child: Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Text(plan.name, style: AppTextStyles.h3),
                        if (isCurrent) ...[
                          const SizedBox(width: 6),
                          Text('(current)', style: AppTextStyles.labelSmall),
                        ],
                      ],
                    ),
                    Text(
                      '${plan.durationDays} days · '
                      '${plan.isUnlimited ? "Unlimited" : "${plan.leadAllotment}"} leads',
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ),
              Text(
                plan.isTrial ? 'FREE' : formatRupees(plan.priceInrPaise),
                style: AppTextStyles.h3.copyWith(
                  color: AppColors.postbookPrimary,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _PaymentHistory extends StatelessWidget {
  const _PaymentHistory({required this.items, required this.plans});

  final List<SubscriptionPayment> items;
  final List<SubscriptionPlan> plans;

  String _planName(String planId) {
    for (final p in plans) {
      if (p.id == planId) return p.name;
    }
    return 'Plan';
  }

  Color _statusColor(String s) {
    switch (s) {
      case 'verified':
      case 'completed':
        return AppColors.statusSuccess;
      case 'pending':
      case 'awaiting_proof':
        return AppColors.statusWarning;
      case 'rejected':
      case 'failed':
        return AppColors.statusError;
      default:
        return AppColors.textTertiary;
    }
  }

  String _methodLabel(String m) {
    switch (m) {
      case 'wallet':
        return 'Wallet';
      case 'upi':
        return 'UPI';
      case 'manual_proof':
        return 'Manual proof';
      default:
        return m;
    }
  }

  @override
  Widget build(BuildContext context) {
    if (items.isEmpty) {
      return Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Text(
          'No subscription payments yet.',
          style: AppTextStyles.bodySmall,
        ),
      );
    }
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          for (var i = 0; i < items.length; i++) ...[
            if (i > 0)
              const Divider(
                height: 1,
                color: AppColors.borderSubtle,
                indent: 12,
                endIndent: 12,
              ),
            _PaymentRow(
              item: items[i],
              planName: _planName(items[i].planId),
              statusColor: _statusColor(items[i].status),
              methodLabel: _methodLabel(items[i].paymentMethod),
            ),
          ],
        ],
      ),
    );
  }
}

class _PaymentRow extends StatelessWidget {
  const _PaymentRow({
    required this.item,
    required this.planName,
    required this.statusColor,
    required this.methodLabel,
  });

  final SubscriptionPayment item;
  final String planName;
  final Color statusColor;
  final String methodLabel;

  @override
  Widget build(BuildContext context) {
    final d = item.createdAt;
    final dt = '${d.day}/${d.month}/${d.year}';
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(planName, style: AppTextStyles.label),
                const SizedBox(height: 2),
                Text(
                  '$methodLabel · $dt',
                  style: AppTextStyles.bodySmall,
                ),
              ],
            ),
          ),
          Column(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              Text(
                formatRupees(item.amountPaise),
                style: AppTextStyles.label,
              ),
              const SizedBox(height: 2),
              Container(
                padding: const EdgeInsets.symmetric(
                  horizontal: 8,
                  vertical: 2,
                ),
                decoration: BoxDecoration(
                  color: statusColor.withValues(alpha: 0.18),
                  borderRadius: BorderRadius.circular(99),
                ),
                child: Text(
                  item.status,
                  style: AppTextStyles.labelSmall.copyWith(color: statusColor),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
