// Bill-pay scheduled payments — Phase 2.
//
// Lists every scheduled-payment rule for the current user. Each row shows the
// account label, schedule kind (monthly / weekly / one_time), next-run date,
// and an active toggle. Trash icon deletes. The FAB opens an "add scheduled"
// bottom sheet.
//
// Telemetry: `billpayScheduledCreated({scheduleKind})` on add. The schedule
// kind is categorical, not PII. Account ids and amounts are never logged.
//
// PRIVACY: surfaces only `accountLabel` (user-chosen text, already stored
// verbatim by the backend) and the masked identifier from the matching
// `BillAccount` lookup.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/billpay.dart';
import 'package:atpost_app/data/repositories/billpay_repository.dart';
import 'package:atpost_app/providers/billpay_providers.dart';
import 'package:atpost_app/services/billpay_telemetry.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class BillPayScheduledScreen extends ConsumerStatefulWidget {
  const BillPayScheduledScreen({super.key});

  @override
  ConsumerState<BillPayScheduledScreen> createState() =>
      _BillPayScheduledScreenState();
}

class _BillPayScheduledScreenState
    extends ConsumerState<BillPayScheduledScreen> {
  Future<void> _refresh() async {
    ref.invalidate(billScheduledProvider);
    ref.invalidate(billAccountsProvider);
    await ref.read(billScheduledProvider.future);
  }

  Future<void> _toggleActive(ScheduledPayment s, bool next) async {
    final repo = ref.read(billpayRepositoryProvider);
    try {
      await repo.updateScheduled(s.id, isActive: next);
      if (!mounted) return;
      ref.invalidate(billScheduledProvider);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not update: $e')),
      );
    }
  }

  Future<void> _delete(ScheduledPayment s) async {
    final repo = ref.read(billpayRepositoryProvider);
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Delete schedule?', style: AppTextStyles.h3),
        content: Text(
          'Future runs will stop. You can still pay the bill manually.',
          style: AppTextStyles.body,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text(
              'Delete',
              style: TextStyle(color: AppColors.statusError),
            ),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await repo.deleteScheduled(s.id);
      if (!mounted) return;
      ref.invalidate(billScheduledProvider);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Schedule deleted')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not delete: $e')),
      );
    }
  }

  Future<void> _openAddSheet() async {
    final accounts = await ref.read(billAccountsProvider.future);
    if (!mounted) return;
    if (accounts.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Add a saved biller first to schedule payments.'),
        ),
      );
      return;
    }
    final added = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(
          top: Radius.circular(AppSpacing.radiusXL),
        ),
      ),
      builder: (_) => _AddScheduledSheet(accounts: accounts),
    );
    if (added == true && mounted) {
      ref.invalidate(billScheduledProvider);
    }
  }

  @override
  Widget build(BuildContext context) {
    final scheduled = ref.watch(billScheduledProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Scheduled payments', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            size: 18,
            color: AppColors.textPrimary,
          ),
          onPressed: () =>
              context.canPop() ? context.pop() : context.go('/billpay'),
        ),
      ),
      floatingActionButton: FloatingActionButton.extended(
        backgroundColor: AppColors.postbookPrimary,
        onPressed: _openAddSheet,
        icon: const Icon(Icons.event_repeat_rounded, color: Colors.white),
        label: const Text(
          'New schedule',
          style: TextStyle(color: Colors.white),
        ),
      ),
      body: RefreshIndicator(
        onRefresh: _refresh,
        color: AppColors.postbookPrimary,
        child: scheduled.when(
          loading: () => const Center(
            child: CircularProgressIndicator(
              color: AppColors.postbookPrimary,
            ),
          ),
          error: (e, _) => ListView(
            children: [
              const SizedBox(height: 100),
              Center(
                child: Padding(
                  padding: const EdgeInsets.all(AppSpacing.xxl),
                  child: Text(
                    'Could not load schedules.\n$e',
                    textAlign: TextAlign.center,
                    style: AppTextStyles.body,
                  ),
                ),
              ),
            ],
          ),
          data: (items) {
            if (items.isEmpty) {
              return ListView(
                children: [
                  const SizedBox(height: 80),
                  Icon(
                    Icons.event_busy_rounded,
                    size: 56,
                    color: AppColors.textDim,
                  ),
                  const SizedBox(height: AppSpacing.l),
                  Text(
                    'No scheduled payments',
                    textAlign: TextAlign.center,
                    style: AppTextStyles.h3,
                  ),
                  const SizedBox(height: AppSpacing.s),
                  Text(
                    'Set it once. We\'ll pay every cycle from your wallet.',
                    textAlign: TextAlign.center,
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              );
            }
            return ListView.separated(
              padding: const EdgeInsets.fromLTRB(
                AppSpacing.xxl,
                AppSpacing.l,
                AppSpacing.xxl,
                AppSpacing.xxxxl + 56,
              ),
              itemCount: items.length,
              separatorBuilder: (_, __) =>
                  const SizedBox(height: AppSpacing.l),
              itemBuilder: (_, i) {
                final s = items[i];
                return _ScheduledCard(
                  schedule: s,
                  onToggle: (v) => _toggleActive(s, v),
                  onDelete: () => _delete(s),
                );
              },
            );
          },
        ),
      ),
    );
  }
}

class _ScheduledCard extends StatelessWidget {
  const _ScheduledCard({
    required this.schedule,
    required this.onToggle,
    required this.onDelete,
  });

  final ScheduledPayment schedule;
  final ValueChanged<bool> onToggle;
  final VoidCallback onDelete;

  @override
  Widget build(BuildContext context) {
    final amountText = schedule.amountPaise == null
        ? 'Full bill amount'
        : formatRupees(schedule.amountPaise!);
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
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                width: 40,
                height: 40,
                decoration: BoxDecoration(
                  color: AppColors.bgSecondary,
                  borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
                ),
                child: const Icon(
                  Icons.schedule_send_rounded,
                  color: AppColors.postbookPrimary,
                ),
              ),
              const SizedBox(width: AppSpacing.l),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      schedule.accountLabel.isEmpty
                          ? 'Saved biller'
                          : schedule.accountLabel,
                      style: AppTextStyles.h3,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                    const SizedBox(height: 2),
                    Text(
                      '${_kindLabel(schedule.scheduleKind)} · '
                      '${schedule.paymentMethod.toUpperCase()} · '
                      '$amountText',
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ),
              Switch(
                value: schedule.isActive,
                activeColor: AppColors.postbookPrimary,
                onChanged: onToggle,
              ),
            ],
          ),
          const SizedBox(height: AppSpacing.l),
          Row(
            children: [
              const Icon(
                Icons.event_rounded,
                size: 16,
                color: AppColors.textTertiary,
              ),
              const SizedBox(width: AppSpacing.s),
              Expanded(
                child: Text(
                  'Next run · ${_fmtDate(schedule.nextRunDate)}',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textTertiary,
                  ),
                ),
              ),
              IconButton(
                icon: const Icon(
                  Icons.delete_outline_rounded,
                  color: AppColors.statusError,
                  size: 20,
                ),
                onPressed: onDelete,
                tooltip: 'Delete',
              ),
            ],
          ),
        ],
      ),
    );
  }
}

String _kindLabel(String kind) {
  switch (kind) {
    case 'monthly':
      return 'Monthly';
    case 'weekly':
      return 'Weekly';
    case 'one_time':
      return 'One-time';
    default:
      return kind;
  }
}

String _fmtDate(DateTime d) {
  const months = [
    'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
    'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
  ];
  final local = d.toLocal();
  return '${local.day} ${months[local.month - 1]} ${local.year}';
}

// ─── Add scheduled bottom sheet ───────────────────────────────────────────

class _AddScheduledSheet extends ConsumerStatefulWidget {
  const _AddScheduledSheet({required this.accounts});

  final List<BillAccount> accounts;

  @override
  ConsumerState<_AddScheduledSheet> createState() => _AddScheduledSheetState();
}

class _AddScheduledSheetState extends ConsumerState<_AddScheduledSheet> {
  String? _selectedAccountId;
  String _scheduleKind = 'monthly';
  String _paymentMethod = 'wallet';
  bool _payFullBill = true;
  final TextEditingController _amountCtrl = TextEditingController();
  DateTime _nextRun = DateTime.now().add(const Duration(days: 1));
  bool _submitting = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _selectedAccountId = widget.accounts.first.id;
  }

  @override
  void dispose() {
    _amountCtrl.dispose();
    super.dispose();
  }

  Future<void> _pickDate() async {
    final picked = await showDatePicker(
      context: context,
      initialDate: _nextRun,
      firstDate: DateTime.now(),
      lastDate: DateTime.now().add(const Duration(days: 365)),
      builder: (ctx, child) => Theme(
        data: Theme.of(ctx).copyWith(
          colorScheme: const ColorScheme.dark(
            primary: AppColors.postbookPrimary,
            onPrimary: Colors.white,
            surface: AppColors.bgSecondary,
            onSurface: AppColors.textPrimary,
          ),
        ),
        child: child!,
      ),
    );
    if (picked != null) {
      setState(() => _nextRun = picked);
    }
  }

  int? _parsePaise() {
    final raw = _amountCtrl.text.trim();
    if (raw.isEmpty) return null;
    final rupees = double.tryParse(raw);
    if (rupees == null || rupees <= 0) return null;
    return (rupees * 100).round();
  }

  Future<void> _submit() async {
    final id = _selectedAccountId;
    if (id == null) return;
    int? amountPaise;
    if (!_payFullBill) {
      amountPaise = _parsePaise();
      if (amountPaise == null) {
        setState(() => _error = 'Enter a valid amount');
        return;
      }
    }
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      final repo = ref.read(billpayRepositoryProvider);
      await repo.addScheduled(
        accountId: id,
        amountPaise: amountPaise,
        paymentMethod: _paymentMethod,
        scheduleKind: _scheduleKind,
        nextRunDate: _nextRun,
      );
      ref.read(billpayTelemetryProvider).billpayScheduledCreated(
            scheduleKind: _scheduleKind,
          );
      if (!mounted) return;
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = 'Could not save: $e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final mq = MediaQuery.of(context);
    return Padding(
      padding: EdgeInsets.only(
        left: AppSpacing.xxl,
        right: AppSpacing.xxl,
        top: AppSpacing.xxl,
        bottom: AppSpacing.xxl + mq.viewInsets.bottom,
      ),
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Center(
              child: Container(
                width: 40,
                height: 4,
                decoration: BoxDecoration(
                  color: AppColors.borderMedium,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),
            const SizedBox(height: AppSpacing.xxl),
            Text('New scheduled payment', style: AppTextStyles.h2),
            const SizedBox(height: AppSpacing.l),
            Text('Biller', style: AppTextStyles.label),
            const SizedBox(height: AppSpacing.s),
            DropdownButtonFormField<String>(
              value: _selectedAccountId,
              dropdownColor: AppColors.bgTertiary,
              isExpanded: true,
              decoration: _fieldDecoration(),
              style: AppTextStyles.bodyMedium.copyWith(
                color: AppColors.textPrimary,
              ),
              items: [
                for (final a in widget.accounts)
                  DropdownMenuItem(
                    value: a.id,
                    child: Text(
                      '${a.label} · ${a.maskedIdentifier}',
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
              ],
              onChanged: _submitting
                  ? null
                  : (v) => setState(() => _selectedAccountId = v),
            ),
            const SizedBox(height: AppSpacing.xxl),
            Text('Repeat', style: AppTextStyles.label),
            const SizedBox(height: AppSpacing.s),
            Wrap(
              spacing: AppSpacing.s,
              children: [
                _kindChip('monthly', 'Monthly'),
                _kindChip('weekly', 'Weekly'),
                _kindChip('one_time', 'One-time'),
              ],
            ),
            const SizedBox(height: AppSpacing.xxl),
            Text('Pay from', style: AppTextStyles.label),
            const SizedBox(height: AppSpacing.s),
            Wrap(
              spacing: AppSpacing.s,
              children: [
                _methodChip('wallet', 'Wallet'),
                _methodChip('upi', 'UPI'),
              ],
            ),
            const SizedBox(height: AppSpacing.xxl),
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: Text('Pay full bill amount', style: AppTextStyles.label),
              subtitle: Text(
                'We\'ll pay whatever the latest bill is.',
                style: AppTextStyles.bodySmall,
              ),
              value: _payFullBill,
              activeColor: AppColors.postbookPrimary,
              onChanged: _submitting
                  ? null
                  : (v) => setState(() => _payFullBill = v),
            ),
            if (!_payFullBill) ...[
              const SizedBox(height: AppSpacing.s),
              TextField(
                controller: _amountCtrl,
                enabled: !_submitting,
                keyboardType:
                    const TextInputType.numberWithOptions(decimal: true),
                inputFormatters: [
                  FilteringTextInputFormatter.allow(RegExp(r'[0-9.]')),
                ],
                style: AppTextStyles.bodyMedium.copyWith(
                  color: AppColors.textPrimary,
                ),
                decoration: _fieldDecoration().copyWith(
                  hintText: 'Amount (₹)',
                  hintStyle:
                      AppTextStyles.bodyMedium.copyWith(
                    color: AppColors.textDim,
                  ),
                ),
              ),
            ],
            const SizedBox(height: AppSpacing.xxl),
            Text('Next run', style: AppTextStyles.label),
            const SizedBox(height: AppSpacing.s),
            InkWell(
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              onTap: _submitting ? null : _pickDate,
              child: Container(
                padding: const EdgeInsets.all(AppSpacing.l),
                decoration: BoxDecoration(
                  color: AppColors.bgTertiary,
                  borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                ),
                child: Row(
                  children: [
                    const Icon(
                      Icons.event_rounded,
                      color: AppColors.textTertiary,
                    ),
                    const SizedBox(width: AppSpacing.l),
                    Text(
                      _fmtDate(_nextRun),
                      style: AppTextStyles.bodyMedium.copyWith(
                        color: AppColors.textPrimary,
                      ),
                    ),
                  ],
                ),
              ),
            ),
            if (_error != null) ...[
              const SizedBox(height: AppSpacing.l),
              Text(
                _error!,
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.statusError,
                ),
              ),
            ],
            const SizedBox(height: AppSpacing.xxl),
            SizedBox(
              width: double.infinity,
              child: ElevatedButton(
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  padding: const EdgeInsets.symmetric(vertical: 14),
                ),
                onPressed: _submitting ? null : _submit,
                child: _submitting
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.white,
                        ),
                      )
                    : const Text(
                        'Create schedule',
                        style: TextStyle(color: Colors.white),
                      ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  InputDecoration _fieldDecoration() {
    return InputDecoration(
      filled: true,
      fillColor: AppColors.bgTertiary,
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        borderSide: BorderSide.none,
      ),
      contentPadding: const EdgeInsets.symmetric(
        horizontal: AppSpacing.l,
        vertical: AppSpacing.l,
      ),
    );
  }

  Widget _kindChip(String value, String label) {
    final selected = _scheduleKind == value;
    return ChoiceChip(
      label: Text(label),
      selected: selected,
      onSelected: _submitting
          ? null
          : (_) => setState(() => _scheduleKind = value),
      backgroundColor: AppColors.bgTertiary,
      selectedColor: AppColors.postbookPrimary.withValues(alpha: 0.25),
      labelStyle: AppTextStyles.bodySmall.copyWith(
        color: selected ? AppColors.textPrimary : AppColors.textSecondary,
      ),
      side: BorderSide(
        color: selected ? AppColors.postbookPrimary : AppColors.borderSubtle,
      ),
    );
  }

  Widget _methodChip(String value, String label) {
    final selected = _paymentMethod == value;
    return ChoiceChip(
      label: Text(label),
      selected: selected,
      onSelected: _submitting
          ? null
          : (_) => setState(() => _paymentMethod = value),
      backgroundColor: AppColors.bgTertiary,
      selectedColor: AppColors.postbookPrimary.withValues(alpha: 0.25),
      labelStyle: AppTextStyles.bodySmall.copyWith(
        color: selected ? AppColors.textPrimary : AppColors.textSecondary,
      ),
      side: BorderSide(
        color: selected ? AppColors.postbookPrimary : AppColors.borderSubtle,
      ),
    );
  }
}
