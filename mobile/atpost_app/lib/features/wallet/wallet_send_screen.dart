// Wallet send — Phase 2 Sprint 1.
//
// Multi-step P2P send:
//   step 0 (recipient): tabs Frequent / AtPost user / Phone number.
//   step 1 (amount + label): amount input + optional label.
//   step 2 (confirm): review card; tap "Send" to fire mutation.
//   step 3 (result): success or failure surface; CTA back to wallet home.
//
// IDEMPOTENCY: the notifier mints a fresh UUID for the `idempotency_key`
// on every send call. Double-taps before the response lands are dedup'd
// server-side.
//
// PRIVACY: telemetry events bucket the amount and pass only the recipient
// type enum (`frequent | atpost_user | phone`).

import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/models/wallet.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/providers/wallet_providers.dart';
import 'package:atpost_app/services/wallet_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class WalletSendScreen extends ConsumerStatefulWidget {
  const WalletSendScreen({super.key, this.preset});

  /// Optional preset from a `Frequent` carousel tap. Map keys:
  /// `recipient_user_id`, `recipient_phone`, `label`, `source`.
  final Map<String, dynamic>? preset;

  @override
  ConsumerState<WalletSendScreen> createState() => _WalletSendScreenState();
}

enum _SendStep { recipient, amount, confirm, result }

enum _RecipientMode { frequent, atpostUser, phone }

class _WalletSendScreenState extends ConsumerState<WalletSendScreen>
    with SingleTickerProviderStateMixin {
  _SendStep _step = _SendStep.recipient;
  late final TabController _tab;

  // Recipient
  String? _recipientUserId;
  String? _recipientPhone;
  String? _recipientLabel;
  _RecipientMode _recipientMode = _RecipientMode.frequent;

  // Amount + label
  final _amountCtrl = TextEditingController();
  final _labelCtrl = TextEditingController();

  // Result
  WalletSendResult? _result;
  String? _errorMessage;

  @override
  void initState() {
    super.initState();
    _tab = TabController(length: 3, vsync: this);
    _tab.addListener(() {
      if (_tab.indexIsChanging) return;
      setState(() {
        _recipientMode = _RecipientMode.values[_tab.index];
      });
    });
    final p = widget.preset;
    if (p != null) {
      _recipientUserId = p['recipient_user_id']?.toString();
      _recipientPhone = p['recipient_phone']?.toString();
      _recipientLabel = p['label']?.toString();
      if ((_recipientUserId != null && _recipientUserId!.isNotEmpty) ||
          (_recipientPhone != null && _recipientPhone!.isNotEmpty)) {
        _step = _SendStep.amount;
      }
    }
  }

  @override
  void dispose() {
    _tab.dispose();
    _amountCtrl.dispose();
    _labelCtrl.dispose();
    super.dispose();
  }

  int? get _amountPaise {
    final raw = _amountCtrl.text.trim();
    final r = int.tryParse(raw);
    if (r == null || r <= 0) return null;
    return r * 100;
  }

  String get _recipientType {
    switch (_recipientMode) {
      case _RecipientMode.frequent:
        return 'frequent';
      case _RecipientMode.atpostUser:
        return 'atpost_user';
      case _RecipientMode.phone:
        return 'phone';
    }
  }

  String get _recipientDisplay {
    if (_recipientLabel != null && _recipientLabel!.isNotEmpty) {
      return _recipientLabel!;
    }
    if (_recipientPhone != null && _recipientPhone!.isNotEmpty) {
      return _recipientPhone!;
    }
    if (_recipientUserId != null && _recipientUserId!.isNotEmpty) {
      return _recipientUserId!;
    }
    return 'Recipient';
  }

  Future<void> _send() async {
    final amount = _amountPaise;
    if (amount == null) return;
    ref.read(walletTelemetryProvider).walletSendStarted(
          recipientType: _recipientType,
          amountPaise: amount,
        );
    final result = await ref.read(walletNotifier.notifier).send(
          recipientUserId: _recipientUserId,
          recipientPhone: _recipientPhone,
          amountPaise: amount,
          label: _labelCtrl.text.trim().isEmpty
              ? _recipientLabel
              : _labelCtrl.text.trim(),
        );
    if (!mounted) return;
    if (result == null) {
      setState(() {
        _step = _SendStep.result;
        _errorMessage = 'Could not send. Please retry.';
      });
      ref.read(walletTelemetryProvider).walletSendFailed(
            recipientType: _recipientType,
            amountPaise: amount,
            reason: 'request_failed',
          );
      return;
    }
    setState(() {
      _result = result;
      _step = _SendStep.result;
    });
    if (result.status == 'succeeded' || result.status == 'pending') {
      ref.read(walletTelemetryProvider).walletSendCompleted(
            recipientType: _recipientType,
            amountPaise: amount,
          );
      ref.invalidate(walletBalanceProvider);
      ref.invalidate(walletRecipientsProvider);
      ref.invalidate(
        walletTransactionsProvider(const TransactionsQuery(limit: 5)),
      );
    } else {
      ref.read(walletTelemetryProvider).walletSendFailed(
            recipientType: _recipientType,
            amountPaise: amount,
            reason: result.failureReason ?? result.status,
          );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text(_titleForStep(), style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
          onPressed: () {
            if (_step == _SendStep.recipient || _step == _SendStep.result) {
              context.pop();
            } else {
              setState(() {
                _step = _SendStep
                    .values[(_step.index - 1).clamp(0, _SendStep.values.length - 1)];
              });
            }
          },
        ),
      ),
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(AppSpacing.l),
          child: switch (_step) {
            _SendStep.recipient => _buildRecipientStep(),
            _SendStep.amount => _buildAmountStep(),
            _SendStep.confirm => _buildConfirmStep(),
            _SendStep.result => _buildResultStep(),
          },
        ),
      ),
    );
  }

  String _titleForStep() {
    switch (_step) {
      case _SendStep.recipient:
        return 'Send to';
      case _SendStep.amount:
        return 'How much?';
      case _SendStep.confirm:
        return 'Confirm';
      case _SendStep.result:
        return 'Done';
    }
  }

  // ─── Step 0: recipient ─────────────────────────────────────────────────

  Widget _buildRecipientStep() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TabBar(
          controller: _tab,
          labelColor: AppColors.posttubePrimary,
          unselectedLabelColor: AppColors.textTertiary,
          indicatorColor: AppColors.posttubePrimary,
          tabs: const [
            Tab(text: 'Frequent'),
            Tab(text: 'AtPost user'),
            Tab(text: 'Phone'),
          ],
        ),
        const SizedBox(height: AppSpacing.l),
        Expanded(
          child: TabBarView(
            controller: _tab,
            children: [
              _FrequentRecipientsList(onPick: _onPickRecipient),
              _AtPostUserPicker(onPick: _onPickAtPostUser),
              _PhonePicker(onConfirm: _onPickPhone),
            ],
          ),
        ),
      ],
    );
  }

  void _onPickRecipient(WalletRecipient r) {
    setState(() {
      _recipientUserId = r.userId;
      _recipientPhone = r.phone;
      _recipientLabel = r.label;
      _step = _SendStep.amount;
    });
  }

  void _onPickPhone(String phone) {
    setState(() {
      _recipientUserId = null;
      _recipientPhone = phone;
      _recipientLabel = null;
      _step = _SendStep.amount;
    });
  }

  void _onPickAtPostUser(User u) {
    setState(() {
      _recipientUserId = u.id;
      _recipientPhone = null;
      _recipientLabel = u.displayName.isNotEmpty ? u.displayName : u.username;
      _step = _SendStep.amount;
    });
  }

  // ─── Step 1: amount ────────────────────────────────────────────────────

  Widget _buildAmountStep() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Container(
          padding: const EdgeInsets.all(AppSpacing.l),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            children: [
              const Icon(Icons.person_outline, color: AppColors.textTertiary),
              const SizedBox(width: AppSpacing.l),
              Expanded(
                child: Text(_recipientDisplay, style: AppTextStyles.label),
              ),
              TextButton(
                onPressed: () => setState(() => _step = _SendStep.recipient),
                child: Text(
                  'Change',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.posttubePrimary,
                  ),
                ),
              ),
            ],
          ),
        ),
        const SizedBox(height: AppSpacing.l),
        Container(
          padding: const EdgeInsets.symmetric(
            horizontal: AppSpacing.xxl,
            vertical: AppSpacing.xxl,
          ),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            children: [
              Text('₹', style: AppTextStyles.h1.copyWith(fontSize: 32)),
              const SizedBox(width: AppSpacing.s),
              Expanded(
                child: TextField(
                  controller: _amountCtrl,
                  keyboardType: TextInputType.number,
                  inputFormatters: [FilteringTextInputFormatter.digitsOnly],
                  style: AppTextStyles.h1.copyWith(fontSize: 32),
                  decoration: InputDecoration(
                    hintText: '0',
                    hintStyle: AppTextStyles.h1.copyWith(
                      fontSize: 32,
                      color: AppColors.textGhost,
                    ),
                    border: InputBorder.none,
                  ),
                  onChanged: (_) => setState(() {}),
                ),
              ),
            ],
          ),
        ),
        const SizedBox(height: AppSpacing.l),
        TextField(
          controller: _labelCtrl,
          maxLength: 60,
          style: AppTextStyles.body,
          decoration: InputDecoration(
            hintText: 'Add a note (optional)',
            hintStyle: AppTextStyles.body.copyWith(color: AppColors.textGhost),
            filled: true,
            fillColor: AppColors.bgCard,
            border: OutlineInputBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              borderSide: const BorderSide(color: AppColors.borderSubtle),
            ),
          ),
        ),
        const Spacer(),
        SizedBox(
          height: 52,
          child: FilledButton(
            onPressed: _amountPaise == null
                ? null
                : () => setState(() => _step = _SendStep.confirm),
            style: FilledButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
              ),
            ),
            child: Text(
              'Continue',
              style: AppTextStyles.h3.copyWith(color: Colors.white),
            ),
          ),
        ),
      ],
    );
  }

  // ─── Step 2: confirm ───────────────────────────────────────────────────

  Widget _buildConfirmStep() {
    final amount = _amountPaise ?? 0;
    final inFlight = ref.watch(walletNotifier).isInFlight;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Container(
          padding: const EdgeInsets.all(AppSpacing.xxl),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            children: [
              Text('Sending', style: AppTextStyles.bodySmall),
              const SizedBox(height: AppSpacing.s),
              Text(
                formatRupees(amount),
                style: AppTextStyles.h1.copyWith(fontSize: 36),
              ),
              const SizedBox(height: AppSpacing.l),
              Text('to', style: AppTextStyles.bodySmall),
              const SizedBox(height: 4),
              Text(_recipientDisplay, style: AppTextStyles.h3),
              if ((_labelCtrl.text.isNotEmpty)) ...[
                const SizedBox(height: AppSpacing.l),
                Text(
                  '"${_labelCtrl.text}"',
                  style: AppTextStyles.bodySmall,
                ),
              ],
            ],
          ),
        ),
        const Spacer(),
        SizedBox(
          height: 52,
          child: FilledButton(
            onPressed: inFlight ? null : _send,
            style: FilledButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
              ),
            ),
            child: Text(
              inFlight ? 'Sending…' : 'Send ${formatRupees(amount)}',
              style: AppTextStyles.h3.copyWith(color: Colors.white),
            ),
          ),
        ),
        const SizedBox(height: AppSpacing.s),
        TextButton(
          onPressed: inFlight
              ? null
              : () => setState(() => _step = _SendStep.amount),
          child: const Text(
            'Back',
            style: TextStyle(color: AppColors.textTertiary),
          ),
        ),
      ],
    );
  }

  // ─── Step 3: result ────────────────────────────────────────────────────

  Widget _buildResultStep() {
    final ok = _result != null &&
        (_result!.status == 'succeeded' || _result!.status == 'pending');
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Container(
          padding: const EdgeInsets.all(AppSpacing.xxl),
          decoration: BoxDecoration(
            color: ok
                ? AppColors.statusSuccess.withAlpha(36)
                : AppColors.statusError.withAlpha(36),
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(
              color: ok
                  ? AppColors.statusSuccess.withAlpha(80)
                  : AppColors.statusError.withAlpha(80),
            ),
          ),
          child: Column(
            children: [
              Icon(
                ok ? Icons.check_circle : Icons.error_outline,
                color: ok ? AppColors.statusSuccess : AppColors.statusError,
                size: 48,
              ),
              const SizedBox(height: AppSpacing.l),
              Text(
                ok ? 'Sent!' : 'Could not send',
                style: AppTextStyles.h2.copyWith(
                  color: ok
                      ? AppColors.statusSuccess
                      : AppColors.statusError,
                ),
              ),
              const SizedBox(height: AppSpacing.s),
              Text(
                ok
                    ? '${formatRupees(_amountPaise ?? 0)} to $_recipientDisplay'
                    : (_errorMessage ??
                        _result?.failureReason ??
                        'Please retry.'),
                style: AppTextStyles.body,
                textAlign: TextAlign.center,
              ),
            ],
          ),
        ),
        const Spacer(),
        SizedBox(
          height: 52,
          child: FilledButton(
            onPressed: () {
              if (ok &&
                  _result != null &&
                  _result!.transactionId.isNotEmpty) {
                context.pushReplacement(
                  '/wallet/transactions/${_result!.transactionId}',
                );
              } else {
                context.pop();
              }
            },
            style: FilledButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
              ),
            ),
            child: Text(
              ok ? 'View receipt' : 'Done',
              style: AppTextStyles.h3.copyWith(color: Colors.white),
            ),
          ),
        ),
      ],
    );
  }
}

// ─── Frequent recipients list ────────────────────────────────────────────

class _FrequentRecipientsList extends ConsumerWidget {
  const _FrequentRecipientsList({required this.onPick});

  final void Function(WalletRecipient) onPick;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(walletRecipientsProvider);
    return async.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (e, _) => Center(
        child: Text('Could not load: $e', style: AppTextStyles.bodySmall),
      ),
      data: (list) => list.isEmpty
          ? Center(
              child: Text(
                'No frequent recipients yet.\nSend to anyone to start.',
                style: AppTextStyles.bodySmall,
                textAlign: TextAlign.center,
              ),
            )
          : ListView.separated(
              itemCount: list.length,
              separatorBuilder: (_, _) => const Divider(
                height: 1,
                color: AppColors.borderSubtle,
              ),
              itemBuilder: (context, i) {
                final r = list[i];
                return ListTile(
                  contentPadding: EdgeInsets.zero,
                  onTap: () => onPick(r),
                  leading: CircleAvatar(
                    backgroundColor: AppColors.bgTertiary,
                    child: Text(
                      r.displayName.isNotEmpty
                          ? r.displayName[0].toUpperCase()
                          : '?',
                      style: AppTextStyles.h3,
                    ),
                  ),
                  title: Text(r.displayName, style: AppTextStyles.label),
                  subtitle: Text(
                    '${r.sendCount} sent',
                    style: AppTextStyles.bodySmall,
                  ),
                  trailing: const Icon(
                    Icons.chevron_right,
                    color: AppColors.textTertiary,
                  ),
                );
              },
            ),
    );
  }
}

// ─── AtPost user picker — reuses the existing `userRepository.searchUsers` ─

class _AtPostUserPicker extends ConsumerStatefulWidget {
  const _AtPostUserPicker({required this.onPick});

  final void Function(User) onPick;

  @override
  ConsumerState<_AtPostUserPicker> createState() => _AtPostUserPickerState();
}

class _AtPostUserPickerState extends ConsumerState<_AtPostUserPicker> {
  final _ctrl = TextEditingController();
  Timer? _debounce;
  Future<List<User>>? _future;

  @override
  void dispose() {
    _ctrl.dispose();
    _debounce?.cancel();
    super.dispose();
  }

  void _onChanged(String q) {
    _debounce?.cancel();
    final query = q.trim();
    if (query.isEmpty) {
      setState(() => _future = null);
      return;
    }
    _debounce = Timer(const Duration(milliseconds: 350), () {
      setState(() {
        _future = ref
            .read(userRepositoryProvider)
            .searchUsers(query, limit: 20)
            .then((res) => res.users);
      });
    });
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        const SizedBox(height: AppSpacing.l),
        TextField(
          controller: _ctrl,
          onChanged: _onChanged,
          style: AppTextStyles.body,
          decoration: InputDecoration(
            prefixIcon: const Icon(Icons.search,
                color: AppColors.textTertiary),
            hintText: 'Search AtPost users by name or @handle',
            hintStyle: AppTextStyles.body.copyWith(color: AppColors.textGhost),
            filled: true,
            fillColor: AppColors.bgCard,
            border: OutlineInputBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              borderSide: const BorderSide(color: AppColors.borderSubtle),
            ),
          ),
        ),
        const SizedBox(height: AppSpacing.l),
        Expanded(
          child: _future == null
              ? Center(
                  child: Text(
                    'Type to find a user.',
                    style: AppTextStyles.bodySmall,
                  ),
                )
              : FutureBuilder<List<User>>(
                  future: _future,
                  builder: (context, snap) {
                    if (snap.connectionState == ConnectionState.waiting) {
                      return const Center(
                        child: CircularProgressIndicator(
                          color: AppColors.postbookPrimary,
                        ),
                      );
                    }
                    if (snap.hasError) {
                      return Center(
                        child: Text('Search failed.',
                            style: AppTextStyles.bodySmall),
                      );
                    }
                    final users = snap.data ?? const <User>[];
                    if (users.isEmpty) {
                      return Center(
                        child: Text('No matches.',
                            style: AppTextStyles.bodySmall),
                      );
                    }
                    return ListView.separated(
                      itemCount: users.length,
                      separatorBuilder: (_, _) => const Divider(
                        height: 1,
                        color: AppColors.borderSubtle,
                      ),
                      itemBuilder: (context, i) {
                        final u = users[i];
                        return ListTile(
                          contentPadding: EdgeInsets.zero,
                          onTap: () => widget.onPick(u),
                          leading: CircleAvatar(
                            backgroundColor: AppColors.bgTertiary,
                            child: Text(
                              u.displayName.isNotEmpty
                                  ? u.displayName[0].toUpperCase()
                                  : '?',
                              style: AppTextStyles.h3,
                            ),
                          ),
                          title: Text(
                            u.displayName,
                            style: AppTextStyles.label,
                          ),
                          subtitle: Text(
                            '@${u.username}',
                            style: AppTextStyles.bodySmall,
                          ),
                          trailing: const Icon(
                            Icons.chevron_right,
                            color: AppColors.textTertiary,
                          ),
                        );
                      },
                    );
                  },
                ),
        ),
      ],
    );
  }
}

// ─── Phone picker ────────────────────────────────────────────────────────

class _PhonePicker extends StatefulWidget {
  const _PhonePicker({required this.onConfirm});

  final void Function(String phone) onConfirm;

  @override
  State<_PhonePicker> createState() => _PhonePickerState();
}

class _PhonePickerState extends State<_PhonePicker> {
  final _ctrl = TextEditingController();

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  bool get _valid {
    final raw = _ctrl.text.replaceAll(RegExp(r'\s+'), '');
    return raw.length == 10 && int.tryParse(raw) != null;
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SizedBox(height: AppSpacing.l),
        TextField(
          controller: _ctrl,
          keyboardType: TextInputType.phone,
          inputFormatters: [
            FilteringTextInputFormatter.digitsOnly,
            LengthLimitingTextInputFormatter(10),
          ],
          style: AppTextStyles.h2,
          onChanged: (_) => setState(() {}),
          decoration: InputDecoration(
            prefixText: '+91 ',
            prefixStyle: AppTextStyles.h2,
            hintText: '10-digit number',
            hintStyle: AppTextStyles.body.copyWith(color: AppColors.textGhost),
            filled: true,
            fillColor: AppColors.bgCard,
            border: OutlineInputBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              borderSide: const BorderSide(color: AppColors.borderSubtle),
            ),
          ),
        ),
        const SizedBox(height: AppSpacing.l),
        Text(
          'If the recipient is not yet on AtPost we hold the receive until '
          'their first login (up to 7 days). They get an SMS prompt.',
          style: AppTextStyles.bodySmall,
        ),
        const Spacer(),
        SizedBox(
          height: 52,
          child: FilledButton(
            onPressed: _valid ? () => widget.onConfirm(_ctrl.text) : null,
            style: FilledButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
              ),
            ),
            child: Text(
              'Continue',
              style: AppTextStyles.h3.copyWith(color: Colors.white),
            ),
          ),
        ),
      ],
    );
  }
}
