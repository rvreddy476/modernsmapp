// Bill-pay account detail — Phase 2.
//
// Per-account detail page. Shows the latest bill (auto-fetch on open via
// `billProvider(accountId)`), payment CTA, settings (reminder days picker,
// autopay paywall stub, default toggle), payment history, delete.
//
// PRIVACY: identifier is shown masked (last 4 chars) at the top; only the
// dedicated edit form would surface the full identifier.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/billpay.dart';
import 'package:atpost_app/data/repositories/billpay_repository.dart';
import 'package:atpost_app/features/billpay/billpay_pay_sheet.dart';
import 'package:atpost_app/providers/billpay_providers.dart';
import 'package:atpost_app/services/billpay_telemetry.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class BillPayAccountDetailScreen extends ConsumerStatefulWidget {
  const BillPayAccountDetailScreen({super.key, required this.accountId});

  final String accountId;

  @override
  ConsumerState<BillPayAccountDetailScreen> createState() =>
      _BillPayAccountDetailScreenState();
}

class _BillPayAccountDetailScreenState
    extends ConsumerState<BillPayAccountDetailScreen> {
  bool _firedBillFetched = false;

  Future<void> _refresh() async {
    ref.invalidate(billProvider(widget.accountId));
    ref.invalidate(billAccountsProvider);
    ref.invalidate(billPaymentsProvider(const PaymentsQuery(limit: 10)));
  }

  @override
  Widget build(BuildContext context) {
    final accounts = ref.watch(billAccountsProvider);
    final bill = ref.watch(billProvider(widget.accountId));
    final payments = ref.watch(
      billPaymentsProvider(const PaymentsQuery(limit: 10)),
    );

    BillAccount? account;
    accounts.whenData((list) {
      for (final a in list) {
        if (a.id == widget.accountId) {
          account = a;
          break;
        }
      }
    });

    // Fire once when bill resolves (success or empty).
    bill.whenData((b) {
      if (_firedBillFetched) return;
      _firedBillFetched = true;
      ref.read(billpayTelemetryProvider).billpayBillFetched(
            categoryId: '',
            hasBill: b.billAmountPaise > 0,
          );
    });

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text(
          account?.label ?? 'Biller',
          style: AppTextStyles.h2,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () => context.pop(),
        ),
      ),
      body: account == null
          ? const Center(
              child: CircularProgressIndicator(
                color: AppColors.postbookPrimary,
              ),
            )
          : RefreshIndicator(
              onRefresh: _refresh,
              color: AppColors.postbookPrimary,
              child: ListView(
                padding: const EdgeInsets.all(AppSpacing.l),
                children: [
                  _AccountHeader(account: account!),
                  const SizedBox(height: AppSpacing.xxl),
                  Text('Latest bill', style: AppTextStyles.h2),
                  const SizedBox(height: AppSpacing.s),
                  _BillCard(
                    bill: bill,
                    onPay: (b) async {
                      final id = await showBillPaySheet(
                        context,
                        BillPayRequest(
                          providerId: account!.providerId,
                          providerName: account!.providerName,
                          identifier: account!.identifier,
                          amountPaise: b.billAmountPaise,
                          accountId: account!.id,
                          billId: b.id,
                        ),
                      );
                      if (id != null && context.mounted) {
                        await _refresh();
                        if (context.mounted) {
                          context.push('/billpay/payments/$id');
                        }
                      }
                    },
                    onFetch: () => _refresh(),
                  ),
                  const SizedBox(height: AppSpacing.xxl),
                  Text('Settings', style: AppTextStyles.h2),
                  const SizedBox(height: AppSpacing.s),
                  _SettingsBlock(account: account!),
                  const SizedBox(height: AppSpacing.xxl),
                  Text('Payment history', style: AppTextStyles.h2),
                  const SizedBox(height: AppSpacing.s),
                  payments.when(
                    loading: () => const Padding(
                      padding: EdgeInsets.all(AppSpacing.xxl),
                      child: Center(
                        child: CircularProgressIndicator(
                          color: AppColors.postbookPrimary,
                        ),
                      ),
                    ),
                    error: (_, _) => Text(
                      'Could not load history.',
                      style: AppTextStyles.bodySmall.copyWith(
                        color: AppColors.statusError,
                      ),
                    ),
                    data: (page) {
                      final filtered = page.items
                          .where(
                            (p) => p.accountId == widget.accountId,
                          )
                          .toList();
                      if (filtered.isEmpty) {
                        return Padding(
                          padding: const EdgeInsets.all(AppSpacing.l),
                          child: Text(
                            'No payments for this account yet.',
                            style: AppTextStyles.bodySmall,
                          ),
                        );
                      }
                      return Column(
                        children: [
                          for (final p in filtered.take(10))
                            _HistoryRow(payment: p),
                        ],
                      );
                    },
                  ),
                  const SizedBox(height: AppSpacing.xxl),
                  TextButton.icon(
                    onPressed: () =>
                        _confirmDelete(context, ref, account!),
                    icon: const Icon(
                      Icons.delete_outline_rounded,
                      color: AppColors.statusError,
                    ),
                    label: Text(
                      'Delete account',
                      style: AppTextStyles.bodyMedium.copyWith(
                        color: AppColors.statusError,
                      ),
                    ),
                  ),
                  const SizedBox(height: AppSpacing.xxxxl),
                ],
              ),
            ),
    );
  }

  Future<void> _confirmDelete(
    BuildContext context,
    WidgetRef ref,
    BillAccount account,
  ) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgTertiary,
        title: Text('Delete biller?', style: AppTextStyles.h2),
        content: Text(
          'You will need to add it back from the category list.',
          style: AppTextStyles.bodySmall,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text(
              'Delete',
              style: TextStyle(color: AppColors.statusError),
            ),
          ),
        ],
      ),
    );
    if (ok != true) return;
    try {
      await ref.read(billpayRepositoryProvider).deleteAccount(account.id);
      ref.invalidate(billAccountsProvider);
      if (!context.mounted) return;
      context.pop();
    } catch (e) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not delete: $e')),
      );
    }
  }
}

// ─── Header ───────────────────────────────────────────────────────────────

class _AccountHeader extends StatelessWidget {
  const _AccountHeader({required this.account});

  final BillAccount account;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgTertiary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Container(
            width: 48,
            height: 48,
            clipBehavior: Clip.antiAlias,
            decoration: BoxDecoration(
              color: AppColors.bgSecondary,
              borderRadius: BorderRadius.circular(10),
            ),
            child: (account.providerLogoUrl == null ||
                    account.providerLogoUrl!.isEmpty)
                ? const Icon(
                    Icons.receipt_long_rounded,
                    color: AppColors.textTertiary,
                  )
                : Image.network(
                    account.providerLogoUrl!,
                    fit: BoxFit.cover,
                    errorBuilder: (_, _, _) => const Icon(
                      Icons.receipt_long_rounded,
                      color: AppColors.textTertiary,
                    ),
                  ),
          ),
          const SizedBox(width: AppSpacing.l),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(account.providerName, style: AppTextStyles.h2),
                const SizedBox(height: 2),
                Text(account.maskedIdentifier, style: AppTextStyles.mono),
              ],
            ),
          ),
          if (account.isDefault)
            Container(
              padding: const EdgeInsets.symmetric(
                horizontal: 8,
                vertical: 4,
              ),
              decoration: BoxDecoration(
                color: AppColors.postbookPrimary.withAlpha(40),
                borderRadius: BorderRadius.circular(6),
              ),
              child: Text(
                'DEFAULT',
                style: AppTextStyles.labelTiny.copyWith(
                  color: AppColors.postbookPrimary,
                ),
              ),
            ),
        ],
      ),
    );
  }
}

// ─── Bill card ────────────────────────────────────────────────────────────

class _BillCard extends StatelessWidget {
  const _BillCard({
    required this.bill,
    required this.onPay,
    required this.onFetch,
  });

  final AsyncValue<Bill> bill;
  final void Function(Bill) onPay;
  final VoidCallback onFetch;

  @override
  Widget build(BuildContext context) {
    return bill.when(
      loading: () => Container(
        padding: const EdgeInsets.all(AppSpacing.xxl),
        decoration: BoxDecoration(
          color: AppColors.bgTertiary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
      ),
      error: (_, _) => Container(
        padding: const EdgeInsets.all(AppSpacing.l),
        decoration: BoxDecoration(
          color: AppColors.bgTertiary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'Could not fetch bill',
              style: AppTextStyles.bodyMedium.copyWith(
                color: AppColors.statusError,
              ),
            ),
            const SizedBox(height: AppSpacing.s),
            ElevatedButton(
              onPressed: onFetch,
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.bgSecondary,
              ),
              child: const Text('Fetch bill'),
            ),
          ],
        ),
      ),
      data: (b) {
        if (b.isPaid) {
          return Container(
            padding: const EdgeInsets.all(AppSpacing.l),
            decoration: BoxDecoration(
              color: AppColors.statusSuccess.withAlpha(20),
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              border: Border.all(
                color: AppColors.statusSuccess.withAlpha(60),
              ),
            ),
            child: Row(
              children: [
                const Icon(
                  Icons.check_circle_rounded,
                  color: AppColors.statusSuccess,
                ),
                const SizedBox(width: AppSpacing.l),
                Expanded(
                  child: Text(
                    b.paidAt == null
                        ? 'Already paid'
                        : 'Paid on ${_fmtDate(b.paidAt!)}',
                    style: AppTextStyles.bodyMedium.copyWith(
                      color: AppColors.statusSuccess,
                    ),
                  ),
                ),
              ],
            ),
          );
        }
        if (b.billAmountPaise <= 0) {
          return Container(
            padding: const EdgeInsets.all(AppSpacing.l),
            decoration: BoxDecoration(
              color: AppColors.bgTertiary,
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'No bill outstanding',
                  style: AppTextStyles.bodyMedium.copyWith(
                    color: AppColors.textPrimary,
                  ),
                ),
                const SizedBox(height: AppSpacing.s),
                ElevatedButton(
                  onPressed: onFetch,
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.bgSecondary,
                  ),
                  child: const Text('Fetch latest'),
                ),
              ],
            ),
          );
        }
        return Container(
          padding: const EdgeInsets.all(AppSpacing.xxl),
          decoration: BoxDecoration(
            color: AppColors.bgTertiary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Bill amount',
                style: AppTextStyles.bodySmall,
              ),
              const SizedBox(height: AppSpacing.xs),
              Text(
                formatRupees(b.billAmountPaise),
                style: AppTextStyles.h1,
              ),
              const SizedBox(height: AppSpacing.l),
              if (b.billDueDate != null)
                _RowKv(
                  label: 'Due',
                  value:
                      '${_fmtDate(b.billDueDate!)} · '
                      '${_dueText(b.daysUntilDue)}',
                  valueColor: (b.daysUntilDue ?? 0) < 0
                      ? AppColors.statusError
                      : null,
                ),
              if (b.billPeriodStart != null && b.billPeriodEnd != null)
                _RowKv(
                  label: 'Period',
                  value:
                      '${_fmtDate(b.billPeriodStart!)} – ${_fmtDate(b.billPeriodEnd!)}',
                ),
              if (b.billNumber != null)
                _RowKv(label: 'Bill no', value: b.billNumber!),
              const SizedBox(height: AppSpacing.l),
              ElevatedButton(
                onPressed: () => onPay(b),
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  padding: const EdgeInsets.symmetric(vertical: 14),
                ),
                child: Text('Pay ${formatRupees(b.billAmountPaise)}'),
              ),
            ],
          ),
        );
      },
    );
  }
}

class _RowKv extends StatelessWidget {
  const _RowKv({required this.label, required this.value, this.valueColor});

  final String label;
  final String value;
  final Color? valueColor;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: AppSpacing.xs),
      child: Row(
        children: [
          SizedBox(
            width: 80,
            child: Text(label, style: AppTextStyles.bodySmall),
          ),
          Expanded(
            child: Text(
              value,
              style: AppTextStyles.bodyMedium.copyWith(
                color: valueColor ?? AppColors.textPrimary,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

// ─── Settings ─────────────────────────────────────────────────────────────

class _SettingsBlock extends ConsumerStatefulWidget {
  const _SettingsBlock({required this.account});

  final BillAccount account;

  @override
  ConsumerState<_SettingsBlock> createState() => _SettingsBlockState();
}

class _SettingsBlockState extends ConsumerState<_SettingsBlock> {
  int? _reminderDays;
  String? _reminderId;

  @override
  void initState() {
    super.initState();
    Future.microtask(_loadExistingReminder);
  }

  Future<void> _loadExistingReminder() async {
    try {
      final reminders =
          await ref.read(billpayRepositoryProvider).getReminders();
      if (!mounted) return;
      for (final r in reminders) {
        if (r.accountId == widget.account.id) {
          setState(() {
            _reminderDays = r.daysBeforeDue;
            _reminderId = r.id;
          });
          break;
        }
      }
    } catch (_) {
      // Silent — settings still functional.
    }
  }

  Future<void> _setDays(int days) async {
    final repo = ref.read(billpayRepositoryProvider);
    try {
      if (_reminderId != null) {
        await repo.deleteReminder(_reminderId!);
      }
      final r = await repo.addReminder(
        accountId: widget.account.id,
        daysBeforeDue: days,
        channels: const ['push'],
      );
      ref
          .read(billpayTelemetryProvider)
          .billpayReminderSet(daysBefore: days);
      ref.invalidate(billRemindersProvider);
      if (!mounted) return;
      setState(() {
        _reminderDays = days;
        _reminderId = r.id;
      });
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not set reminder: $e')),
      );
    }
  }

  Future<void> _toggleDefault(bool v) async {
    try {
      await ref.read(billpayRepositoryProvider).updateAccount(
            widget.account.id,
            isDefault: v,
          );
      ref.invalidate(billAccountsProvider);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not update: $e')),
      );
    }
  }

  Future<void> _toggleAutopay(bool v) async {
    if (v) {
      // Premium-gated paywall stub.
      final ok = await showDialog<bool>(
        context: context,
        builder: (ctx) => AlertDialog(
          backgroundColor: AppColors.bgTertiary,
          title: Text('Autopay is a Premium feature', style: AppTextStyles.h2),
          content: Text(
            'Upgrade to VChat Premium to pay bills automatically when they '
            'arrive. You can cancel any time.',
            style: AppTextStyles.bodySmall,
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(ctx).pop(false),
              child: const Text('Maybe later'),
            ),
            TextButton(
              onPressed: () => Navigator.of(ctx).pop(true),
              child: const Text('See plans'),
            ),
          ],
        ),
      );
      if (ok == true && context.mounted) {
        context.push('/pulse/premium');
      }
      return;
    }
    try {
      await ref.read(billpayRepositoryProvider).updateAccount(
            widget.account.id,
            autopayEnabled: false,
          );
      ref.invalidate(billAccountsProvider);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not update: $e')),
      );
    }
  }

  Future<void> _pickDays() async {
    final result = await showModalBottomSheet<int>(
      context: context,
      backgroundColor: AppColors.bgPrimary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
      ),
      builder: (ctx) => _ReminderDaysPicker(current: _reminderDays),
    );
    if (result != null) {
      _setDays(result);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgTertiary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          ListTile(
            leading: const Icon(
              Icons.notifications_outlined,
              color: AppColors.textPrimary,
            ),
            title: Text(
              'Bill reminder',
              style: AppTextStyles.bodyMedium.copyWith(
                color: AppColors.textPrimary,
              ),
            ),
            subtitle: Text(
              _reminderDays == null
                  ? 'Off'
                  : 'Notify $_reminderDays day${_reminderDays == 1 ? '' : 's'} before due',
              style: AppTextStyles.bodySmall,
            ),
            trailing: const Icon(
              Icons.arrow_forward_ios_rounded,
              size: 14,
              color: AppColors.textTertiary,
            ),
            onTap: _pickDays,
          ),
          const Divider(height: 1, color: AppColors.borderSubtle),
          SwitchListTile(
            value: widget.account.autopayEnabled,
            onChanged: _toggleAutopay,
            activeColor: AppColors.postbookPrimary,
            secondary: const Icon(
              Icons.autorenew_rounded,
              color: AppColors.textPrimary,
            ),
            title: Text(
              'Autopay (Premium)',
              style: AppTextStyles.bodyMedium.copyWith(
                color: AppColors.textPrimary,
              ),
            ),
            subtitle: Text(
              'Pay automatically when bill arrives',
              style: AppTextStyles.bodySmall,
            ),
          ),
          const Divider(height: 1, color: AppColors.borderSubtle),
          SwitchListTile(
            value: widget.account.isDefault,
            onChanged: _toggleDefault,
            activeColor: AppColors.postbookPrimary,
            secondary: const Icon(
              Icons.star_outline_rounded,
              color: AppColors.textPrimary,
            ),
            title: Text(
              'Default account',
              style: AppTextStyles.bodyMedium.copyWith(
                color: AppColors.textPrimary,
              ),
            ),
            subtitle: Text(
              'Used for the home screen quick-pay tile',
              style: AppTextStyles.bodySmall,
            ),
          ),
        ],
      ),
    );
  }
}

class _ReminderDaysPicker extends StatelessWidget {
  const _ReminderDaysPicker({required this.current});

  final int? current;

  @override
  Widget build(BuildContext context) {
    const options = [1, 3, 5, 7];
    return Padding(
      padding: const EdgeInsets.fromLTRB(
        AppSpacing.xxl,
        AppSpacing.l,
        AppSpacing.xxl,
        AppSpacing.xxl,
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
          const SizedBox(height: AppSpacing.l),
          Text('Remind me before due', style: AppTextStyles.h2),
          const SizedBox(height: AppSpacing.xxl),
          for (final d in options)
            ListTile(
              title: Text(
                '$d day${d == 1 ? '' : 's'} before',
                style: AppTextStyles.bodyMedium.copyWith(
                  color: AppColors.textPrimary,
                ),
              ),
              trailing: current == d
                  ? const Icon(
                      Icons.check_rounded,
                      color: AppColors.postbookPrimary,
                    )
                  : null,
              onTap: () => Navigator.of(context).pop(d),
            ),
        ],
      ),
    );
  }
}

// ─── History row ──────────────────────────────────────────────────────────

class _HistoryRow extends StatelessWidget {
  const _HistoryRow({required this.payment});

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
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    formatRupees(payment.amountPaise),
                    style: AppTextStyles.bodyMedium.copyWith(
                      color: AppColors.textPrimary,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    _fmtDate(payment.createdAt),
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              ),
            ),
            Text(
              _statusLabel(),
              style: AppTextStyles.labelSmall.copyWith(color: _statusColor()),
            ),
          ],
        ),
      ),
    );
  }
}

// ─── Helpers ──────────────────────────────────────────────────────────────

String _fmtDate(DateTime d) {
  const months = [
    'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
    'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
  ];
  return '${d.day} ${months[d.month - 1]} ${d.year}';
}

String _dueText(int? d) {
  if (d == null) return '';
  if (d < 0) return '${-d} day${-d == 1 ? '' : 's'} overdue';
  if (d == 0) return 'Due today';
  if (d == 1) return 'Due tomorrow';
  return 'In $d days';
}
