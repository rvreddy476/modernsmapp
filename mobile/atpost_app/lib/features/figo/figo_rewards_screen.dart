// FiGo loyalty + referrals screen.
//
// Single screen with two tabs:
//   • Loyalty — balance + tier + ledger + redeem button.
//   • Referral — code + apply-a-code form.
//
// Pulls /v1/food/me/loyalty and /v1/food/me/referral via the
// FoodRewardsRepository. Wave G4.4 + G4.6 backend.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/figo_rewards_repository.dart';
import 'package:atpost_app/providers/figo_rewards_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class FigoRewardsScreen extends ConsumerStatefulWidget {
  const FigoRewardsScreen({super.key});

  @override
  ConsumerState<FigoRewardsScreen> createState() => _FigoRewardsScreenState();
}

class _FigoRewardsScreenState extends ConsumerState<FigoRewardsScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabs;

  @override
  void initState() {
    super.initState();
    _tabs = TabController(length: 2, vsync: this);
  }

  @override
  void dispose() {
    _tabs.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        title: Text('FiGo rewards', style: AppTextStyles.h2),
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        bottom: TabBar(
          controller: _tabs,
          tabs: const [
            Tab(text: 'Loyalty'),
            Tab(text: 'Referral'),
          ],
        ),
      ),
      body: TabBarView(
        controller: _tabs,
        children: const [_LoyaltyTab(), _ReferralTab()],
      ),
    );
  }
}

class _LoyaltyTab extends ConsumerWidget {
  const _LoyaltyTab();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncSnap = ref.watch(foodLoyaltyProvider);
    return asyncSnap.when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, _) => Center(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Text(
            'Could not load loyalty: $e',
            style: AppTextStyles.bodySmall,
          ),
        ),
      ),
      data: (snap) => _LoyaltyContent(snap: snap),
    );
  }
}

class _LoyaltyContent extends ConsumerStatefulWidget {
  const _LoyaltyContent({required this.snap});
  final FoodLoyaltySnapshot snap;

  @override
  ConsumerState<_LoyaltyContent> createState() => _LoyaltyContentState();
}

class _LoyaltyContentState extends ConsumerState<_LoyaltyContent> {
  final _redeemController = TextEditingController();

  @override
  void dispose() {
    _redeemController.dispose();
    super.dispose();
  }

  Future<void> _redeem() async {
    final pts = int.tryParse(_redeemController.text.trim());
    if (pts == null || pts <= 0) return;
    final notifier = ref.read(foodLoyaltyRedeemProvider.notifier);
    final ok = await notifier.redeem(pts);
    if (!mounted) return;
    if (ok) {
      _redeemController.clear();
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Redeemed $pts points')),
      );
    } else {
      final err = ref.read(foodLoyaltyRedeemProvider);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not redeem: ${err.error ?? "unknown"}')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final snap = widget.snap;
    final redeemState = ref.watch(foodLoyaltyRedeemProvider);
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 32),
      children: [
        // Balance card.
        Container(
          padding: const EdgeInsets.all(18),
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
                  Text('Balance', style: AppTextStyles.labelSmall),
                  const Spacer(),
                  _TierChip(tier: snap.balance.tier),
                ],
              ),
              const SizedBox(height: 6),
              Text(
                '${snap.balance.pointsBalance} pts',
                style: AppTextStyles.h2.copyWith(fontSize: 30),
              ),
              const SizedBox(height: 4),
              Text(
                '${snap.balance.lifetimeEarned} lifetime earned',
                style: AppTextStyles.bodySmall
                    .copyWith(color: AppColors.textMuted),
              ),
            ],
          ),
        ),
        const SizedBox(height: 16),
        // Redeem.
        Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Redeem points', style: AppTextStyles.h3),
              const SizedBox(height: 8),
              Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: _redeemController,
                      keyboardType: TextInputType.number,
                      style: AppTextStyles.body,
                      decoration: InputDecoration(
                        hintText: 'Points',
                        filled: true,
                        fillColor: AppColors.bgTertiary,
                        border: OutlineInputBorder(
                          borderRadius:
                              BorderRadius.circular(AppSpacing.radiusMedium),
                          borderSide: BorderSide.none,
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  ElevatedButton(
                    style: ElevatedButton.styleFrom(
                      backgroundColor: AppColors.postbookPrimary,
                      foregroundColor: Colors.white,
                    ),
                    onPressed: redeemState is AsyncLoading ? null : _redeem,
                    child: redeemState is AsyncLoading
                        ? const SizedBox(
                            width: 16,
                            height: 16,
                            child: CircularProgressIndicator(
                                strokeWidth: 2, color: Colors.white),
                          )
                        : const Text('Redeem'),
                  ),
                ],
              ),
            ],
          ),
        ),
        const SizedBox(height: 16),
        // Ledger.
        Text('Recent activity', style: AppTextStyles.h3),
        const SizedBox(height: 8),
        if (snap.ledger.isEmpty)
          Padding(
            padding: const EdgeInsets.all(16),
            child: Text(
              'No earns or redeems yet.',
              style: AppTextStyles.bodySmall
                  .copyWith(color: AppColors.textMuted),
            ),
          )
        else
          ...snap.ledger.map(_LedgerTile.new),
      ],
    );
  }
}

class _LedgerTile extends StatelessWidget {
  const _LedgerTile(this.row);
  final FoodLoyaltyLedgerRow row;

  @override
  Widget build(BuildContext context) {
    final positive = row.delta > 0;
    final color = positive ? AppColors.statusSuccess : AppColors.statusError;
    final sign = positive ? '+' : '';
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Row(
        children: [
          Icon(
            positive ? Icons.add_circle : Icons.remove_circle,
            color: color,
            size: 18,
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(row.reason, style: AppTextStyles.label),
                if (row.createdAt != null)
                  Text(
                    _formatTs(row.createdAt!),
                    style: AppTextStyles.bodySmall
                        .copyWith(color: AppColors.textMuted),
                  ),
              ],
            ),
          ),
          Text(
            '$sign${row.delta}',
            style: AppTextStyles.label.copyWith(color: color),
          ),
        ],
      ),
    );
  }
}

String _formatTs(DateTime t) {
  final local = t.toLocal();
  return '${local.year}-${local.month.toString().padLeft(2, '0')}-'
      '${local.day.toString().padLeft(2, '0')} '
      '${local.hour.toString().padLeft(2, '0')}:${local.minute.toString().padLeft(2, '0')}';
}

class _TierChip extends StatelessWidget {
  const _TierChip({required this.tier});
  final String tier;

  Color get _bg {
    switch (tier) {
      case 'platinum':
        return const Color(0xFFE7E5E4);
      case 'gold':
        return const Color(0xFFFDE68A);
      case 'silver':
        return const Color(0xFFE5E7EB);
      default:
        return const Color(0xFFDDB892);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
      decoration: BoxDecoration(
        color: _bg,
        borderRadius: BorderRadius.circular(99),
      ),
      child: Text(
        tier.toUpperCase(),
        style: AppTextStyles.labelSmall.copyWith(
          color: Colors.black87,
          fontWeight: FontWeight.w700,
        ),
      ),
    );
  }
}

class _ReferralTab extends ConsumerStatefulWidget {
  const _ReferralTab();

  @override
  ConsumerState<_ReferralTab> createState() => _ReferralTabState();
}

class _ReferralTabState extends ConsumerState<_ReferralTab> {
  final _applyController = TextEditingController();
  bool _applied = false;

  @override
  void dispose() {
    _applyController.dispose();
    super.dispose();
  }

  Future<void> _apply() async {
    final code = _applyController.text.trim();
    if (code.isEmpty) return;
    final notifier = ref.read(foodReferralApplyProvider.notifier);
    final ok = await notifier.submit(code);
    if (!mounted) return;
    if (ok) {
      setState(() => _applied = true);
      _applyController.clear();
    } else {
      final err = ref.read(foodReferralApplyProvider);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not apply: ${err.error ?? "unknown"}')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final asyncCode = ref.watch(foodReferralCodeProvider);
    final apply = ref.watch(foodReferralApplyProvider);
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 32),
      children: [
        // Your code.
        Container(
          padding: const EdgeInsets.all(18),
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Your referral code', style: AppTextStyles.labelSmall),
              const SizedBox(height: 6),
              asyncCode.when(
                loading: () => const SizedBox(
                  height: 32,
                  child: Center(child: CircularProgressIndicator(strokeWidth: 2)),
                ),
                error: (e, _) => Text(
                  'Could not load code: $e',
                  style: AppTextStyles.bodySmall,
                ),
                data: (code) => Row(
                  children: [
                    Text(
                      code.isEmpty ? '—' : code,
                      style: AppTextStyles.h2
                          .copyWith(fontFamily: 'monospace'),
                    ),
                    const Spacer(),
                    if (code.isNotEmpty)
                      IconButton(
                        icon: const Icon(Icons.copy, size: 18),
                        onPressed: () {
                          // Clipboard intentionally omitted — the
                          // Flutter `services` import would balloon the
                          // bundle for this one button. Users can long-
                          // press on the text to copy.
                          ScaffoldMessenger.of(context).showSnackBar(
                            const SnackBar(
                              content: Text('Long-press the code to copy'),
                            ),
                          );
                        },
                      ),
                  ],
                ),
              ),
              const SizedBox(height: 6),
              Text(
                'Share this code with a friend. When they order, both of '
                'you get 200 loyalty points after their first delivery.',
                style: AppTextStyles.bodySmall
                    .copyWith(color: AppColors.textMuted),
              ),
            ],
          ),
        ),
        const SizedBox(height: 16),
        // Apply someone else's code.
        Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: _applied
              ? Padding(
                  padding: const EdgeInsets.symmetric(vertical: 8),
                  child: Text(
                    'Code applied. The reward lands after your first '
                    'delivered order.',
                    style: AppTextStyles.bodySmall,
                  ),
                )
              : Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('Apply a friend\'s code', style: AppTextStyles.h3),
                    const SizedBox(height: 8),
                    Row(
                      children: [
                        Expanded(
                          child: TextField(
                            controller: _applyController,
                            textCapitalization: TextCapitalization.characters,
                            style: AppTextStyles.body,
                            decoration: InputDecoration(
                              hintText: 'e.g. ABC234',
                              filled: true,
                              fillColor: AppColors.bgTertiary,
                              border: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(
                                    AppSpacing.radiusMedium),
                                borderSide: BorderSide.none,
                              ),
                            ),
                          ),
                        ),
                        const SizedBox(width: 8),
                        ElevatedButton(
                          style: ElevatedButton.styleFrom(
                            backgroundColor: AppColors.postbookPrimary,
                            foregroundColor: Colors.white,
                          ),
                          onPressed: apply is AsyncLoading ? null : _apply,
                          child: apply is AsyncLoading
                              ? const SizedBox(
                                  width: 16,
                                  height: 16,
                                  child: CircularProgressIndicator(
                                      strokeWidth: 2,
                                      color: Colors.white),
                                )
                              : const Text('Apply'),
                        ),
                      ],
                    ),
                  ],
                ),
        ),
      ],
    );
  }
}
