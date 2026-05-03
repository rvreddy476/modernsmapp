// Wallet home — Phase 2 Sprint 1.
//
// Hero balance + KYC tier chip + frozen banner + two CTAs (Add Money /
// Send Money) + recent 5 transactions + frequent recipients carousel +
// pull-to-refresh.
//
// PRIVACY: every paise value displayed runs through `formatRupees`. We
// never log balances; the only telemetry fired here is `walletOpened`.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/wallet.dart';
import 'package:atpost_app/providers/wallet_providers.dart';
import 'package:atpost_app/services/wallet_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:shimmer/shimmer.dart';

class WalletHomeScreen extends ConsumerStatefulWidget {
  const WalletHomeScreen({super.key});

  @override
  ConsumerState<WalletHomeScreen> createState() => _WalletHomeScreenState();
}

class _WalletHomeScreenState extends ConsumerState<WalletHomeScreen> {
  bool _firedOpened = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_firedOpened) return;
      _firedOpened = true;
      ref.read(walletTelemetryProvider).walletOpened();
    });
  }

  Future<void> _refresh() async {
    ref.invalidate(walletBalanceProvider);
    ref.invalidate(walletRecipientsProvider);
    ref.invalidate(walletTransactionsProvider(const TransactionsQuery(limit: 5)));
    await ref.read(walletBalanceProvider.future);
  }

  @override
  Widget build(BuildContext context) {
    final balance = ref.watch(walletBalanceProvider);
    final recents = ref.watch(
      walletTransactionsProvider(const TransactionsQuery(limit: 5)),
    );
    final recipients = ref.watch(walletRecipientsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Wallet', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
          onPressed: () => context.canPop() ? context.pop() : context.go('/'),
        ),
        actions: [
          balance.maybeWhen(
            data: (b) => _TierChip(tier: b.kycTier, label: b.tierChipLabel),
            orElse: () => const SizedBox.shrink(),
          ),
          IconButton(
            icon: const Icon(Icons.history, color: AppColors.textPrimary),
            onPressed: () => context.push('/wallet/transactions'),
          ),
          const SizedBox(width: AppSpacing.s),
        ],
      ),
      body: RefreshIndicator(
        color: AppColors.postbookPrimary,
        onRefresh: _refresh,
        child: ListView(
          padding: const EdgeInsets.symmetric(
            horizontal: AppSpacing.l,
            vertical: AppSpacing.l,
          ),
          children: [
            balance.when(
              loading: () => const _BalanceSkeleton(),
              error: (e, _) => _BalanceError(error: e, onRetry: _refresh),
              data: (b) => _BalanceCard(balance: b),
            ),
            const SizedBox(height: AppSpacing.l),
            balance.maybeWhen(
              data: (b) => b.isFrozen
                  ? _FrozenBanner(reason: b.frozenReason)
                  : const SizedBox.shrink(),
              orElse: () => const SizedBox.shrink(),
            ),
            const SizedBox(height: AppSpacing.l),
            _CtaRow(
              isFrozen: balance.maybeWhen(
                data: (b) => b.isFrozen,
                orElse: () => false,
              ),
            ),
            const SizedBox(height: AppSpacing.xxl),
            _SectionHeader(
              title: 'Frequent recipients',
              actionLabel: 'See all',
              onAction: () => context.push('/wallet/send'),
            ),
            const SizedBox(height: AppSpacing.s),
            SizedBox(
              height: 96,
              child: recipients.when(
                loading: () => const _RecipientsSkeleton(),
                error: (_, _) => const _EmptyHint(
                  text: 'Could not load recipients.',
                ),
                data: (list) => list.isEmpty
                    ? const _EmptyHint(text: 'No frequent recipients yet.')
                    : _RecipientsCarousel(recipients: list),
              ),
            ),
            const SizedBox(height: AppSpacing.xxl),
            _SectionHeader(
              title: 'Recent activity',
              actionLabel: 'View all',
              onAction: () => context.push('/wallet/transactions'),
            ),
            const SizedBox(height: AppSpacing.s),
            recents.when(
              loading: () => const _TxnsSkeleton(),
              error: (_, _) => const _EmptyHint(
                text: 'Could not load transactions.',
              ),
              data: (page) => page.items.isEmpty
                  ? const _EmptyHint(text: 'No transactions yet.')
                  : Column(
                      children: [
                        for (final t in page.items.take(5))
                          _TxnRow(txn: t),
                      ],
                    ),
            ),
            const SizedBox(height: AppSpacing.xxxxl),
          ],
        ),
      ),
    );
  }
}

// ─── Balance card ─────────────────────────────────────────────────────────

class _BalanceCard extends StatelessWidget {
  const _BalanceCard({required this.balance});

  final WalletBalance balance;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.xxl),
      decoration: BoxDecoration(
        gradient: AppColors.ctaGradient,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Available balance',
            style: AppTextStyles.bodySmall.copyWith(
              color: Colors.white.withAlpha(220),
            ),
          ),
          const SizedBox(height: AppSpacing.s),
          Text(
            formatRupees(balance.availablePaise),
            style: AppTextStyles.h1.copyWith(color: Colors.white, fontSize: 36),
          ),
          const SizedBox(height: AppSpacing.l),
          Row(
            children: [
              if (balance.pendingInPaise > 0)
                Expanded(
                  child: Shimmer.fromColors(
                    baseColor: Colors.white.withAlpha(180),
                    highlightColor: Colors.white,
                    period: const Duration(milliseconds: 1500),
                    child: _PendingChip(
                      label: 'Receiving',
                      value: formatRupees(balance.pendingInPaise),
                    ),
                  ),
                ),
              if (balance.pendingInPaise > 0 && balance.pendingOutPaise > 0)
                const SizedBox(width: AppSpacing.s),
              if (balance.pendingOutPaise > 0)
                Expanded(
                  child: _PendingChip(
                    label: 'Sending',
                    value: formatRupees(balance.pendingOutPaise),
                  ),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

class _PendingChip extends StatelessWidget {
  const _PendingChip({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(
        horizontal: AppSpacing.l,
        vertical: AppSpacing.s,
      ),
      decoration: BoxDecoration(
        color: Colors.white.withAlpha(40),
        borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            label,
            style: AppTextStyles.labelSmall.copyWith(
              color: Colors.white.withAlpha(230),
            ),
          ),
          const SizedBox(width: 6),
          Text(
            value,
            style: AppTextStyles.label.copyWith(color: Colors.white),
          ),
        ],
      ),
    );
  }
}

class _BalanceSkeleton extends StatelessWidget {
  const _BalanceSkeleton();

  @override
  Widget build(BuildContext context) {
    return Shimmer.fromColors(
      baseColor: AppColors.bgSecondary,
      highlightColor: AppColors.bgTertiary,
      child: Container(
        height: 152,
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        ),
      ),
    );
  }
}

class _BalanceError extends StatelessWidget {
  const _BalanceError({required this.error, required this.onRetry});

  final Object error;
  final Future<void> Function() onRetry;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.xxl),
      decoration: BoxDecoration(
        color: AppColors.statusError.withAlpha(36),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.statusError.withAlpha(80)),
      ),
      child: Column(
        children: [
          Text(
            'Could not load wallet',
            style: AppTextStyles.h3.copyWith(color: AppColors.statusError),
          ),
          const SizedBox(height: AppSpacing.s),
          Text('$error', style: AppTextStyles.bodySmall),
          const SizedBox(height: AppSpacing.l),
          ElevatedButton(
            onPressed: onRetry,
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
            ),
            child: const Text('Retry'),
          ),
        ],
      ),
    );
  }
}

// ─── Tier chip ────────────────────────────────────────────────────────────

class _TierChip extends StatelessWidget {
  const _TierChip({required this.tier, required this.label});

  final String tier;
  final String label;

  @override
  Widget build(BuildContext context) {
    final color = switch (tier) {
      'enhanced' => AppColors.statusSuccess,
      'full' => AppColors.posttubePrimary,
      _ => AppColors.statusWarning,
    };
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 10),
      child: GestureDetector(
        onTap: () => GoRouter.of(context).push('/wallet/kyc'),
        child: Container(
          padding: const EdgeInsets.symmetric(
            horizontal: AppSpacing.l,
            vertical: AppSpacing.xs,
          ),
          decoration: BoxDecoration(
            color: color.withAlpha(36),
            borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
            border: Border.all(color: color.withAlpha(120)),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(Icons.shield_outlined, color: color, size: 14),
              const SizedBox(width: 4),
              Text(
                label,
                style: AppTextStyles.labelSmall.copyWith(color: color),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

// ─── Frozen banner ────────────────────────────────────────────────────────

class _FrozenBanner extends StatelessWidget {
  const _FrozenBanner({required this.reason});

  final String? reason;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.statusError.withAlpha(36),
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.statusError.withAlpha(100)),
      ),
      child: Row(
        children: [
          const Icon(Icons.lock_outline, color: AppColors.statusError),
          const SizedBox(width: AppSpacing.l),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Wallet temporarily frozen',
                  style: AppTextStyles.h3.copyWith(
                    color: AppColors.statusError,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  reason ?? 'Please contact support to restore access.',
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

// ─── CTA row ──────────────────────────────────────────────────────────────

class _CtaRow extends StatelessWidget {
  const _CtaRow({required this.isFrozen});

  final bool isFrozen;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(
          child: _CtaButton(
            icon: Icons.add_circle_outline,
            label: 'Add Money',
            color: AppColors.postbookPrimary,
            onTap: isFrozen
                ? null
                : () => GoRouter.of(context).push('/wallet/top-up'),
          ),
        ),
        const SizedBox(width: AppSpacing.l),
        Expanded(
          child: _CtaButton(
            icon: Icons.send_outlined,
            label: 'Send Money',
            color: AppColors.posttubePrimary,
            onTap: isFrozen
                ? null
                : () => GoRouter.of(context).push('/wallet/send'),
          ),
        ),
      ],
    );
  }
}

class _CtaButton extends StatelessWidget {
  const _CtaButton({
    required this.icon,
    required this.label,
    required this.color,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final Color color;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final disabled = onTap == null;
    return Material(
      color: disabled ? AppColors.bgSecondary : color.withAlpha(36),
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      child: InkWell(
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 18),
          child: Column(
            children: [
              Icon(icon, color: disabled ? AppColors.textGhost : color),
              const SizedBox(height: 6),
              Text(
                label,
                style: AppTextStyles.label.copyWith(
                  color: disabled ? AppColors.textGhost : color,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

// ─── Section header ───────────────────────────────────────────────────────

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({
    required this.title,
    required this.actionLabel,
    required this.onAction,
  });

  final String title;
  final String actionLabel;
  final VoidCallback onAction;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(child: Text(title, style: AppTextStyles.h3)),
        TextButton(
          onPressed: onAction,
          child: Text(
            actionLabel,
            style: AppTextStyles.label.copyWith(
              color: AppColors.posttubePrimary,
            ),
          ),
        ),
      ],
    );
  }
}

// ─── Recipients carousel ─────────────────────────────────────────────────

class _RecipientsCarousel extends StatelessWidget {
  const _RecipientsCarousel({required this.recipients});

  final List<WalletRecipient> recipients;

  @override
  Widget build(BuildContext context) {
    return ListView.separated(
      scrollDirection: Axis.horizontal,
      itemCount: recipients.length,
      separatorBuilder: (_, _) => const SizedBox(width: AppSpacing.l),
      itemBuilder: (context, i) {
        final r = recipients[i];
        return GestureDetector(
          onTap: () {
            GoRouter.of(context).push(
              '/wallet/send',
              extra: {
                'recipient_user_id': r.userId,
                'recipient_phone': r.phone,
                'label': r.label,
                'source': 'frequent',
              },
            );
          },
          child: SizedBox(
            width: 76,
            child: Column(
              children: [
                CircleAvatar(
                  radius: 28,
                  backgroundColor: AppColors.bgTertiary,
                  child: Text(
                    r.displayName.isNotEmpty
                        ? r.displayName[0].toUpperCase()
                        : '?',
                    style: AppTextStyles.h2,
                  ),
                ),
                const SizedBox(height: 6),
                Text(
                  r.displayName,
                  style: AppTextStyles.bodySmall,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ],
            ),
          ),
        );
      },
    );
  }
}

class _RecipientsSkeleton extends StatelessWidget {
  const _RecipientsSkeleton();

  @override
  Widget build(BuildContext context) {
    return ListView.separated(
      scrollDirection: Axis.horizontal,
      itemCount: 5,
      separatorBuilder: (_, _) => const SizedBox(width: AppSpacing.l),
      itemBuilder: (_, _) => Shimmer.fromColors(
        baseColor: AppColors.bgSecondary,
        highlightColor: AppColors.bgTertiary,
        child: const CircleAvatar(
          radius: 28,
          backgroundColor: AppColors.bgSecondary,
        ),
      ),
    );
  }
}

// ─── Transaction row ─────────────────────────────────────────────────────

class _TxnRow extends StatelessWidget {
  const _TxnRow({required this.txn});

  final WalletTransaction txn;

  @override
  Widget build(BuildContext context) {
    final credit = txn.isCredit;
    final iconData = switch (txn.type) {
      'top_up' => Icons.account_balance_wallet_outlined,
      'send' => Icons.north_east,
      'receive' => Icons.south_west,
      'merchant_pay' => Icons.shopping_bag_outlined,
      'refund' => Icons.replay_outlined,
      'reversal' => Icons.undo,
      _ => Icons.swap_horiz,
    };
    return InkWell(
      onTap: () => GoRouter.of(context).push('/wallet/transactions/${txn.id}'),
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: AppSpacing.m),
        child: Row(
          children: [
            CircleAvatar(
              radius: 20,
              backgroundColor: AppColors.bgSecondary,
              child: Icon(iconData, color: AppColors.textSecondary, size: 18),
            ),
            const SizedBox(width: AppSpacing.l),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    _label(txn),
                    style: AppTextStyles.label,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    _subtitle(txn),
                    style: AppTextStyles.bodySmall,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ),
            ),
            const SizedBox(width: AppSpacing.s),
            Column(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Text(
                  '${credit ? '+' : '-'}${formatRupees(txn.amountPaise)}',
                  style: AppTextStyles.label.copyWith(
                    color: credit
                        ? AppColors.statusSuccess
                        : AppColors.textPrimary,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  _statusBadge(txn.status),
                  style: AppTextStyles.labelSmall.copyWith(
                    color: _statusColor(txn.status),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  String _label(WalletTransaction t) {
    switch (t.type) {
      case 'top_up':
        return 'Added to wallet';
      case 'send':
        return 'Sent to ${t.counterpartyLabel ?? t.counterpartyPhone ?? 'recipient'}';
      case 'receive':
        return 'Received from ${t.counterpartyLabel ?? t.counterpartyPhone ?? 'sender'}';
      case 'merchant_pay':
        return 'Paid to ${t.merchantService ?? 'merchant'}';
      case 'refund':
        return 'Refund';
      case 'reversal':
        return 'Reversed';
      default:
        return t.type;
    }
  }

  String _subtitle(WalletTransaction t) {
    final d = t.createdAt.toLocal();
    final m = d.month.toString().padLeft(2, '0');
    final day = d.day.toString().padLeft(2, '0');
    final hh = d.hour.toString().padLeft(2, '0');
    final mm = d.minute.toString().padLeft(2, '0');
    return '$day-$m · $hh:$mm';
  }

  String _statusBadge(String s) {
    switch (s) {
      case 'succeeded':
        return 'Success';
      case 'pending':
        return 'Pending';
      case 'failed':
        return 'Failed';
      case 'reversed':
        return 'Reversed';
      default:
        return s;
    }
  }

  Color _statusColor(String s) {
    switch (s) {
      case 'succeeded':
        return AppColors.statusSuccess;
      case 'pending':
        return AppColors.statusWarning;
      case 'failed':
      case 'reversed':
        return AppColors.statusError;
      default:
        return AppColors.textTertiary;
    }
  }
}

class _TxnsSkeleton extends StatelessWidget {
  const _TxnsSkeleton();

  @override
  Widget build(BuildContext context) {
    return Shimmer.fromColors(
      baseColor: AppColors.bgSecondary,
      highlightColor: AppColors.bgTertiary,
      child: Column(
        children: List.generate(
          5,
          (_) => Container(
            margin: const EdgeInsets.symmetric(vertical: 6),
            height: 56,
            decoration: BoxDecoration(
              color: AppColors.bgSecondary,
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            ),
          ),
        ),
      ),
    );
  }
}

class _EmptyHint extends StatelessWidget {
  const _EmptyHint({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: AppSpacing.l),
      child: Center(
        child: Text(text, style: AppTextStyles.bodySmall),
      ),
    );
  }
}
