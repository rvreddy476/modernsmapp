// Bill-pay reminders — Phase 2.
//
// Lists every reminder the user has configured across all saved billers.
// Each row shows the account label (NOT the raw identifier — only the masked
// last-4 — see PRIVACY note below), days-before-due, and active channels.
//
// FAB opens a bottom sheet to add a new reminder against an existing account.
// Long-press / trash icon deletes a reminder. Telemetry fires on add only —
// the days-before integer is non-PII; account_id is NEVER logged.
//
// PRIVACY:
//   * Only `account.label` and `account.maskedIdentifier` are rendered. The
//     full identifier never reaches this surface.
//   * `billpayReminderSet({daysBefore})` is the only event emitted; it
//     contains no account id, no provider id, no identifier.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/billpay.dart';
import 'package:atpost_app/data/repositories/billpay_repository.dart';
import 'package:atpost_app/providers/billpay_providers.dart';
import 'package:atpost_app/services/billpay_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class BillPayRemindersScreen extends ConsumerStatefulWidget {
  const BillPayRemindersScreen({super.key});

  @override
  ConsumerState<BillPayRemindersScreen> createState() =>
      _BillPayRemindersScreenState();
}

class _BillPayRemindersScreenState
    extends ConsumerState<BillPayRemindersScreen> {
  Future<void> _refresh() async {
    ref.invalidate(billRemindersProvider);
    ref.invalidate(billAccountsProvider);
    await ref.read(billRemindersProvider.future);
  }

  Future<void> _openAddSheet() async {
    final accounts = await ref.read(billAccountsProvider.future);
    if (!mounted) return;
    if (accounts.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Add a saved biller first to enable reminders.'),
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
      builder: (_) => _AddReminderSheet(accounts: accounts),
    );
    if (added == true && mounted) {
      ref.invalidate(billRemindersProvider);
    }
  }

  Future<void> _delete(BillReminder r) async {
    final repo = ref.read(billpayRepositoryProvider);
    try {
      await repo.deleteReminder(r.id);
      if (!mounted) return;
      ref.invalidate(billRemindersProvider);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Reminder removed')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not delete: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final reminders = ref.watch(billRemindersProvider);
    final accounts = ref.watch(billAccountsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Reminders', style: AppTextStyles.h2),
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
        icon: const Icon(Icons.add_alarm_rounded, color: Colors.white),
        label: const Text(
          'Add reminder',
          style: TextStyle(color: Colors.white),
        ),
      ),
      body: RefreshIndicator(
        onRefresh: _refresh,
        color: AppColors.postbookPrimary,
        child: reminders.when(
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
                    'Could not load reminders.\n$e',
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
                    Icons.notifications_none_rounded,
                    size: 56,
                    color: AppColors.textDim,
                  ),
                  const SizedBox(height: AppSpacing.l),
                  Text(
                    'No reminders set yet',
                    textAlign: TextAlign.center,
                    style: AppTextStyles.h3,
                  ),
                  const SizedBox(height: AppSpacing.s),
                  Text(
                    'We\'ll nudge you N days before each bill is due.',
                    textAlign: TextAlign.center,
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              );
            }
            // Build label lookup from accounts (best-effort).
            final labels = <String, BillAccount>{};
            accounts.whenData((list) {
              for (final a in list) {
                labels[a.id] = a;
              }
            });
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
                final r = items[i];
                final acc = labels[r.accountId];
                return _ReminderCard(
                  reminder: r,
                  account: acc,
                  onDelete: () => _delete(r),
                );
              },
            );
          },
        ),
      ),
    );
  }
}

class _ReminderCard extends StatelessWidget {
  const _ReminderCard({
    required this.reminder,
    required this.account,
    required this.onDelete,
  });

  final BillReminder reminder;
  final BillAccount? account;
  final VoidCallback onDelete;

  @override
  Widget build(BuildContext context) {
    final label = account?.label ?? 'Saved biller';
    final masked = account?.maskedIdentifier ?? '';
    final channelText = reminder.channels.isEmpty
        ? 'push'
        : reminder.channels.join(', ');
    return Container(
      padding: const EdgeInsets.all(AppSpacing.xxl),
      decoration: BoxDecoration(
        color: AppColors.bgTertiary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
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
              Icons.notifications_active_rounded,
              color: AppColors.postbookPrimary,
            ),
          ),
          const SizedBox(width: AppSpacing.l),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  label,
                  style: AppTextStyles.h3,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                if (masked.isNotEmpty) ...[
                  const SizedBox(height: 2),
                  Text(masked, style: AppTextStyles.bodySmall),
                ],
                const SizedBox(height: AppSpacing.s),
                Text(
                  '${reminder.daysBeforeDue} days before due · $channelText',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textTertiary,
                  ),
                ),
              ],
            ),
          ),
          IconButton(
            icon: const Icon(
              Icons.delete_outline_rounded,
              color: AppColors.statusError,
            ),
            tooltip: 'Delete',
            onPressed: onDelete,
          ),
        ],
      ),
    );
  }
}

// ─── Add reminder bottom sheet ────────────────────────────────────────────

class _AddReminderSheet extends ConsumerStatefulWidget {
  const _AddReminderSheet({required this.accounts});

  final List<BillAccount> accounts;

  @override
  ConsumerState<_AddReminderSheet> createState() => _AddReminderSheetState();
}

class _AddReminderSheetState extends ConsumerState<_AddReminderSheet> {
  String? _selectedAccountId;
  int _daysBefore = 3;
  final Set<String> _channels = {'push'};
  bool _submitting = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _selectedAccountId = widget.accounts.first.id;
  }

  Future<void> _submit() async {
    final id = _selectedAccountId;
    if (id == null) return;
    if (_channels.isEmpty) {
      setState(() => _error = 'Pick at least one channel');
      return;
    }
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      final repo = ref.read(billpayRepositoryProvider);
      await repo.addReminder(
        accountId: id,
        daysBeforeDue: _daysBefore,
        channels: _channels.toList(),
      );
      ref.read(billpayTelemetryProvider).billpayReminderSet(
            daysBefore: _daysBefore,
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
          Text('Add reminder', style: AppTextStyles.h2),
          const SizedBox(height: AppSpacing.l),
          Text('Biller', style: AppTextStyles.label),
          const SizedBox(height: AppSpacing.s),
          DropdownButtonFormField<String>(
            value: _selectedAccountId,
            dropdownColor: AppColors.bgTertiary,
            isExpanded: true,
            decoration: InputDecoration(
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
            ),
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
          Text(
            'Notify me $_daysBefore days before due',
            style: AppTextStyles.label,
          ),
          Slider(
            value: _daysBefore.toDouble(),
            min: 1,
            max: 14,
            divisions: 13,
            activeColor: AppColors.postbookPrimary,
            inactiveColor: AppColors.borderMedium,
            label: '$_daysBefore d',
            onChanged: _submitting
                ? null
                : (v) => setState(() => _daysBefore = v.round()),
          ),
          const SizedBox(height: AppSpacing.s),
          Text('Channels', style: AppTextStyles.label),
          const SizedBox(height: AppSpacing.s),
          Wrap(
            spacing: AppSpacing.s,
            children: [
              _channelChip('push', 'Push'),
              _channelChip('sms', 'SMS'),
              _channelChip('email', 'Email'),
            ],
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
                      'Save reminder',
                      style: TextStyle(color: Colors.white),
                    ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _channelChip(String value, String label) {
    final selected = _channels.contains(value);
    return FilterChip(
      label: Text(label),
      selected: selected,
      onSelected: _submitting
          ? null
          : (s) {
              setState(() {
                if (s) {
                  _channels.add(value);
                } else {
                  _channels.remove(value);
                }
              });
            },
      backgroundColor: AppColors.bgTertiary,
      selectedColor: AppColors.postbookPrimary.withValues(alpha: 0.25),
      checkmarkColor: AppColors.postbookPrimary,
      labelStyle: AppTextStyles.bodySmall.copyWith(
        color: selected ? AppColors.textPrimary : AppColors.textSecondary,
      ),
      side: BorderSide(
        color: selected ? AppColors.postbookPrimary : AppColors.borderSubtle,
      ),
    );
  }
}
