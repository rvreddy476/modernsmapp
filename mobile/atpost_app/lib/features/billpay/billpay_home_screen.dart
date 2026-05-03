// Bill-pay home — Phase 2.
//
// AppBar: "Bills & Recharges". Hero mobile-recharge card; 8-tile category
// grid; saved billers carousel; recent payments list; pull-to-refresh.
//
// PRIVACY: identifiers in the saved-billers carousel render through
// `BillAccount.maskedIdentifier`. Telemetry only fires `billpayHomeOpened`.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/billpay.dart';
import 'package:atpost_app/providers/billpay_providers.dart';
import 'package:atpost_app/services/billpay_telemetry.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class BillPayHomeScreen extends ConsumerStatefulWidget {
  const BillPayHomeScreen({super.key});

  @override
  ConsumerState<BillPayHomeScreen> createState() => _BillPayHomeScreenState();
}

class _BillPayHomeScreenState extends ConsumerState<BillPayHomeScreen> {
  bool _firedOpened = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_firedOpened) return;
      _firedOpened = true;
      ref.read(billpayTelemetryProvider).billpayHomeOpened();
    });
  }

  Future<void> _refresh() async {
    ref.invalidate(billCategoriesProvider);
    ref.invalidate(billAccountsProvider);
    ref.invalidate(
      billPaymentsProvider(const PaymentsQuery(limit: 5)),
    );
    await ref.read(billCategoriesProvider.future);
  }

  @override
  Widget build(BuildContext context) {
    final categories = ref.watch(billCategoriesProvider);
    final accounts = ref.watch(billAccountsProvider);
    final recents = ref.watch(
      billPaymentsProvider(const PaymentsQuery(limit: 5)),
    );

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Bills & Recharges', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () => context.canPop() ? context.pop() : context.go('/'),
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.history, color: AppColors.textPrimary),
            tooltip: 'Payment history',
            onPressed: () => context.push('/billpay/payments'),
          ),
          PopupMenuButton<String>(
            icon: const Icon(
              Icons.more_vert_rounded,
              color: AppColors.textPrimary,
            ),
            color: AppColors.bgTertiary,
            onSelected: (v) {
              if (v == 'reminders') context.push('/billpay/reminders');
              if (v == 'scheduled') context.push('/billpay/scheduled');
            },
            itemBuilder: (_) => const [
              PopupMenuItem(
                value: 'reminders',
                child: Text(
                  'Reminders',
                  style: TextStyle(color: AppColors.textPrimary),
                ),
              ),
              PopupMenuItem(
                value: 'scheduled',
                child: Text(
                  'Scheduled payments',
                  style: TextStyle(color: AppColors.textPrimary),
                ),
              ),
            ],
          ),
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
            const _RechargeHero(),
            const SizedBox(height: AppSpacing.xxl),
            _SectionHeader(title: 'Categories'),
            const SizedBox(height: AppSpacing.m),
            categories.when(
              loading: () => const _CategoriesSkeleton(),
              error: (_, _) => const _ErrorHint(
                text: 'Could not load categories.',
              ),
              data: (list) => _CategoriesGrid(categories: list),
            ),
            const SizedBox(height: AppSpacing.xxl),
            _SectionHeader(
              title: 'Saved billers',
              actionLabel: accounts.maybeWhen(
                data: (l) => l.isEmpty ? null : 'Manage',
                orElse: () => null,
              ),
              onAction: () => context.push('/billpay/payments'),
            ),
            const SizedBox(height: AppSpacing.s),
            SizedBox(
              height: 132,
              child: accounts.when(
                loading: () => const _CarouselSkeleton(),
                error: (_, _) => const _ErrorHint(
                  text: 'Could not load saved billers.',
                ),
                data: (list) => list.isEmpty
                    ? const _EmptyHint(
                        text: 'No saved billers yet. '
                            'Tap a category to add one.',
                      )
                    : _AccountsCarousel(accounts: list),
              ),
            ),
            const SizedBox(height: AppSpacing.xxl),
            _SectionHeader(
              title: 'Recent payments',
              actionLabel: 'View all',
              onAction: () => context.push('/billpay/payments'),
            ),
            const SizedBox(height: AppSpacing.s),
            recents.when(
              loading: () => const _PaymentsSkeleton(),
              error: (_, _) => const _ErrorHint(
                text: 'Could not load recent payments.',
              ),
              data: (page) => page.items.isEmpty
                  ? const _EmptyHint(text: 'No payments yet.')
                  : Column(
                      children: [
                        for (final p in page.items.take(5)) _PaymentRow(payment: p),
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

// ─── Recharge hero ────────────────────────────────────────────────────────

class _RechargeHero extends StatelessWidget {
  const _RechargeHero();

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      onTap: () => context.push('/billpay/recharge'),
      child: Container(
        padding: const EdgeInsets.all(AppSpacing.xxl),
        decoration: BoxDecoration(
          gradient: AppColors.ctaGradient,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        ),
        child: Row(
          children: [
            Container(
              width: 48,
              height: 48,
              decoration: BoxDecoration(
                color: Colors.white.withAlpha(40),
                borderRadius: BorderRadius.circular(12),
              ),
              child: const Icon(
                Icons.smartphone_outlined,
                color: Colors.white,
                size: 24,
              ),
            ),
            const SizedBox(width: AppSpacing.l),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    'Mobile recharge',
                    style: AppTextStyles.h2.copyWith(color: Colors.white),
                  ),
                  const SizedBox(height: AppSpacing.xs),
                  Text(
                    'Prepaid plans, instant top-up',
                    style: AppTextStyles.bodySmall.copyWith(
                      color: Colors.white.withAlpha(220),
                    ),
                  ),
                ],
              ),
            ),
            const Icon(
              Icons.arrow_forward_ios_rounded,
              color: Colors.white,
              size: 16,
            ),
          ],
        ),
      ),
    );
  }
}

// ─── Categories grid ──────────────────────────────────────────────────────

class _CategoriesGrid extends StatelessWidget {
  const _CategoriesGrid({required this.categories});

  final List<BillCategory> categories;

  @override
  Widget build(BuildContext context) {
    final visible = categories.where((c) => c.isActive).toList()
      ..sort((a, b) => a.sortOrder.compareTo(b.sortOrder));
    return GridView.count(
      crossAxisCount: 4,
      mainAxisSpacing: AppSpacing.l,
      crossAxisSpacing: AppSpacing.l,
      childAspectRatio: 0.85,
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      children: [
        for (final c in visible) _CategoryTile(category: c),
      ],
    );
  }
}

class _CategoryTile extends StatelessWidget {
  const _CategoryTile({required this.category});

  final BillCategory category;

  IconData _iconFor(String key) {
    switch (key) {
      case 'mobile_postpaid':
        return Icons.phone_iphone_rounded;
      case 'dth':
        return Icons.satellite_alt_rounded;
      case 'electricity':
        return Icons.bolt_rounded;
      case 'gas':
        return Icons.local_fire_department_rounded;
      case 'water':
        return Icons.water_drop_rounded;
      case 'broadband':
        return Icons.router_rounded;
      case 'fastag':
        return Icons.local_taxi_rounded;
      case 'insurance':
        return Icons.shield_outlined;
      case 'loan_emi':
        return Icons.payments_rounded;
      default:
        return Icons.receipt_long_rounded;
    }
  }

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      onTap: () => context.push('/billpay/category/${category.id}'),
      child: Column(
        children: [
          Container(
            width: 56,
            height: 56,
            decoration: BoxDecoration(
              color: AppColors.bgTertiary,
              borderRadius: BorderRadius.circular(16),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Icon(
              _iconFor(category.icon),
              color: AppColors.postbookPrimary,
              size: 26,
            ),
          ),
          const SizedBox(height: AppSpacing.s),
          Text(
            category.name,
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.textPrimary,
            ),
            textAlign: TextAlign.center,
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
          ),
        ],
      ),
    );
  }
}

// ─── Saved billers carousel ───────────────────────────────────────────────

class _AccountsCarousel extends StatelessWidget {
  const _AccountsCarousel({required this.accounts});

  final List<BillAccount> accounts;

  @override
  Widget build(BuildContext context) {
    return ListView.separated(
      scrollDirection: Axis.horizontal,
      itemCount: accounts.length,
      separatorBuilder: (_, _) => const SizedBox(width: AppSpacing.l),
      itemBuilder: (_, i) => _AccountCard(account: accounts[i]),
    );
  }
}

class _AccountCard extends ConsumerWidget {
  const _AccountCard({required this.account});

  final BillAccount account;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final bill = ref.watch(billProvider(account.id));
    return SizedBox(
      width: 200,
      child: InkWell(
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        onTap: () => context.push('/billpay/account/${account.id}'),
        child: Container(
          padding: const EdgeInsets.all(AppSpacing.l),
          decoration: BoxDecoration(
            color: AppColors.bgTertiary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Row(
                children: [
                  _ProviderLogo(url: account.providerLogoUrl, size: 32),
                  const SizedBox(width: AppSpacing.m),
                  Expanded(
                    child: Text(
                      account.label,
                      style: AppTextStyles.h3,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ],
              ),
              Text(
                account.maskedIdentifier,
                style: AppTextStyles.mono,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              bill.maybeWhen(
                data: (b) {
                  if (b.isPaid) {
                    return _AccountChip(
                      label: 'Paid',
                      bg: AppColors.statusSuccess.withAlpha(40),
                      fg: AppColors.statusSuccess,
                    );
                  }
                  final d = b.daysUntilDue;
                  if (d == null) {
                    return _AccountChip(
                      label: formatRupees(b.billAmountPaise),
                      bg: AppColors.bgCardHover,
                      fg: AppColors.textPrimary,
                    );
                  }
                  if (d < 0) {
                    return _AccountChip(
                      label: 'Overdue',
                      bg: AppColors.statusError.withAlpha(40),
                      fg: AppColors.statusError,
                    );
                  }
                  return _AccountChip(
                    label: 'Due in $d ${d == 1 ? 'day' : 'days'}',
                    bg: AppColors.statusWarning.withAlpha(40),
                    fg: AppColors.statusWarning,
                  );
                },
                orElse: () => _AccountChip(
                  label: 'Tap to fetch bill',
                  bg: AppColors.bgCardHover,
                  fg: AppColors.textTertiary,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _AccountChip extends StatelessWidget {
  const _AccountChip({
    required this.label,
    required this.bg,
    required this.fg,
  });

  final String label;
  final Color bg;
  final Color fg;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(
        horizontal: AppSpacing.m,
        vertical: AppSpacing.xs,
      ),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        label,
        style: AppTextStyles.labelSmall.copyWith(color: fg),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
    );
  }
}

class _ProviderLogo extends StatelessWidget {
  const _ProviderLogo({required this.url, this.size = 36});

  final String? url;
  final double size;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      clipBehavior: Clip.antiAlias,
      child: (url == null || url!.isEmpty)
          ? const Icon(
              Icons.receipt_long_rounded,
              color: AppColors.textTertiary,
              size: 18,
            )
          : Image.network(
              url!,
              fit: BoxFit.cover,
              errorBuilder: (_, _, _) => const Icon(
                Icons.receipt_long_rounded,
                color: AppColors.textTertiary,
                size: 18,
              ),
            ),
    );
  }
}

// ─── Payments list ────────────────────────────────────────────────────────

class _PaymentRow extends StatelessWidget {
  const _PaymentRow({required this.payment});

  final BillPayment payment;

  Color _statusColor() {
    if (payment.isSucceeded) return AppColors.statusSuccess;
    if (payment.isFailed) return AppColors.statusError;
    if (payment.isRefunded) return AppColors.accentPurple;
    return AppColors.statusWarning;
  }

  String _statusLabel() {
    if (payment.isSucceeded) return 'Paid';
    if (payment.isFailed) return 'Failed';
    if (payment.isRefunded) return 'Refunded';
    return 'Pending';
  }

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => context.push('/billpay/payments/${payment.id}'),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: AppSpacing.l),
        child: Row(
          children: [
            _ProviderLogo(url: null, size: 36),
            const SizedBox(width: AppSpacing.l),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    payment.providerName,
                    style: AppTextStyles.h3,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    payment.maskedIdentifier ??
                        _formatDate(payment.createdAt),
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              ),
            ),
            Column(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Text(
                  formatRupees(payment.amountPaise),
                  style: AppTextStyles.h3,
                ),
                const SizedBox(height: 2),
                Text(
                  _statusLabel(),
                  style: AppTextStyles.labelSmall.copyWith(
                    color: _statusColor(),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

String _formatDate(DateTime d) {
  const months = [
    'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
    'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
  ];
  return '${d.day} ${months[d.month - 1]}';
}

// ─── Skeletons / hints ────────────────────────────────────────────────────

class _CategoriesSkeleton extends StatelessWidget {
  const _CategoriesSkeleton();

  @override
  Widget build(BuildContext context) {
    return GridView.count(
      crossAxisCount: 4,
      mainAxisSpacing: AppSpacing.l,
      crossAxisSpacing: AppSpacing.l,
      childAspectRatio: 0.85,
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      children: List.generate(8, (_) {
        return Column(
          children: [
            Container(
              width: 56,
              height: 56,
              decoration: BoxDecoration(
                color: AppColors.bgTertiary,
                borderRadius: BorderRadius.circular(16),
              ),
            ),
            const SizedBox(height: AppSpacing.s),
            Container(
              height: 10,
              width: 56,
              decoration: BoxDecoration(
                color: AppColors.bgTertiary,
                borderRadius: BorderRadius.circular(4),
              ),
            ),
          ],
        );
      }),
    );
  }
}

class _CarouselSkeleton extends StatelessWidget {
  const _CarouselSkeleton();

  @override
  Widget build(BuildContext context) {
    return ListView.separated(
      scrollDirection: Axis.horizontal,
      itemCount: 3,
      separatorBuilder: (_, _) => const SizedBox(width: AppSpacing.l),
      itemBuilder: (_, _) => Container(
        width: 200,
        decoration: BoxDecoration(
          color: AppColors.bgTertiary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        ),
      ),
    );
  }
}

class _PaymentsSkeleton extends StatelessWidget {
  const _PaymentsSkeleton();

  @override
  Widget build(BuildContext context) {
    return Column(
      children: List.generate(3, (_) {
        return Padding(
          padding: const EdgeInsets.symmetric(vertical: AppSpacing.l),
          child: Row(
            children: [
              Container(
                width: 36,
                height: 36,
                decoration: BoxDecoration(
                  color: AppColors.bgTertiary,
                  borderRadius: BorderRadius.circular(8),
                ),
              ),
              const SizedBox(width: AppSpacing.l),
              Expanded(
                child: Container(
                  height: 14,
                  decoration: BoxDecoration(
                    color: AppColors.bgTertiary,
                    borderRadius: BorderRadius.circular(4),
                  ),
                ),
              ),
            ],
          ),
        );
      }),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({
    required this.title,
    this.actionLabel,
    this.onAction,
  });

  final String title;
  final String? actionLabel;
  final VoidCallback? onAction;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(child: Text(title, style: AppTextStyles.h2)),
        if (actionLabel != null && onAction != null)
          TextButton(
            onPressed: onAction,
            child: Text(
              actionLabel!,
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.postbookPrimary,
              ),
            ),
          ),
      ],
    );
  }
}

class _EmptyHint extends StatelessWidget {
  const _EmptyHint({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Text(
        text,
        textAlign: TextAlign.center,
        style: AppTextStyles.bodySmall,
      ),
    );
  }
}

class _ErrorHint extends StatelessWidget {
  const _ErrorHint({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Text(
        text,
        textAlign: TextAlign.center,
        style: AppTextStyles.bodySmall.copyWith(color: AppColors.statusError),
      ),
    );
  }
}
