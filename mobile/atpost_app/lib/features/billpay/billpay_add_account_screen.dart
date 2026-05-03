// Bill-pay add-account screen — Phase 2.
//
// Dynamic form built from `provider.customerParams`. The first param is the
// primary identifier (consumer number, connection id, etc.). Additional
// params land in `extra_params`. Each input shows the friendly param name
// and validates against the server-supplied regex.
//
// On success → calls `addAccount` and routes to the account detail screen.

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

class BillPayAddAccountScreen extends ConsumerStatefulWidget {
  const BillPayAddAccountScreen({super.key, required this.providerId});

  final String providerId;

  @override
  ConsumerState<BillPayAddAccountScreen> createState() =>
      _BillPayAddAccountScreenState();
}

class _BillPayAddAccountScreenState
    extends ConsumerState<BillPayAddAccountScreen> {
  final _formKey = GlobalKey<FormState>();
  final Map<String, TextEditingController> _controllers = {};
  final _labelController = TextEditingController();
  bool _submitting = false;

  @override
  void dispose() {
    for (final c in _controllers.values) {
      c.dispose();
    }
    _labelController.dispose();
    super.dispose();
  }

  TextEditingController _ctrlFor(String paramId) {
    return _controllers.putIfAbsent(paramId, TextEditingController.new);
  }

  Future<void> _save(BillProvider provider) async {
    if (_submitting) return;
    if (!_formKey.currentState!.validate()) return;

    final params = provider.customerParams;
    if (params.isEmpty) return;

    final identifier = _ctrlFor(params.first.id).text.trim();
    final extras = <String, String>{};
    for (var i = 1; i < params.length; i++) {
      final p = params[i];
      final v = _ctrlFor(p.id).text.trim();
      if (v.isNotEmpty) extras[p.id] = v;
    }
    final label = _labelController.text.trim();

    setState(() => _submitting = true);
    try {
      final repo = ref.read(billpayRepositoryProvider);
      final account = await repo.addAccount(
        providerId: provider.id,
        identifier: identifier,
        extraParams: extras,
        label: label.isEmpty ? null : label,
      );
      ref
          .read(billpayTelemetryProvider)
          .billpayAccountAdded(categoryId: provider.categoryId);
      ref.invalidate(billAccountsProvider);
      if (!mounted) return;
      context.pushReplacement('/billpay/account/${account.id}');
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not save: $e')),
      );
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final providerAsync = ref.watch(
      billProviderDetailProvider(widget.providerId),
    );

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Add biller', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () => context.pop(),
        ),
      ),
      body: providerAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(
            color: AppColors.postbookPrimary,
          ),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text(
              'Could not load provider details.',
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.statusError,
              ),
              textAlign: TextAlign.center,
            ),
          ),
        ),
        data: (provider) => _AddAccountForm(
          provider: provider,
          formKey: _formKey,
          submitting: _submitting,
          ctrlFor: _ctrlFor,
          labelController: _labelController,
          onSave: () => _save(provider),
        ),
      ),
    );
  }
}

class _AddAccountForm extends StatelessWidget {
  const _AddAccountForm({
    required this.provider,
    required this.formKey,
    required this.submitting,
    required this.ctrlFor,
    required this.labelController,
    required this.onSave,
  });

  final BillProvider provider;
  final GlobalKey<FormState> formKey;
  final bool submitting;
  final TextEditingController Function(String) ctrlFor;
  final TextEditingController labelController;
  final VoidCallback onSave;

  @override
  Widget build(BuildContext context) {
    return Form(
      key: formKey,
      child: ListView(
        padding: const EdgeInsets.all(AppSpacing.xxl),
        children: [
          _ProviderHeader(provider: provider),
          const SizedBox(height: AppSpacing.xxl),
          for (var i = 0; i < provider.customerParams.length; i++) ...[
            _ParamField(
              param: provider.customerParams[i],
              controller: ctrlFor(provider.customerParams[i].id),
              isPrimary: i == 0,
            ),
            const SizedBox(height: AppSpacing.l),
          ],
          const SizedBox(height: AppSpacing.s),
          Text(
            'Account label (optional)',
            style: AppTextStyles.label.copyWith(color: AppColors.textPrimary),
          ),
          const SizedBox(height: AppSpacing.s),
          TextFormField(
            controller: labelController,
            style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
            decoration: InputDecoration(
              hintText: provider.name,
              hintStyle: AppTextStyles.bodySmall,
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
          ),
          const SizedBox(height: AppSpacing.xs),
          Text(
            'Used in the saved-billers carousel.',
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: AppSpacing.xxxxl),
          ElevatedButton(
            onPressed: submitting ? null : onSave,
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            child: submitting
                ? const SizedBox(
                    height: 18,
                    width: 18,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: Colors.white,
                    ),
                  )
                : const Text('Save account'),
          ),
        ],
      ),
    );
  }
}

class _ProviderHeader extends StatelessWidget {
  const _ProviderHeader({required this.provider});

  final BillProvider provider;

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
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: (provider.logoUrl == null || provider.logoUrl!.isEmpty)
                ? const Icon(
                    Icons.receipt_long_rounded,
                    color: AppColors.textTertiary,
                  )
                : Image.network(
                    provider.logoUrl!,
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
                Text(provider.name, style: AppTextStyles.h2),
                const SizedBox(height: 2),
                Text(
                  provider.billFetchSupported
                      ? 'Auto-fetch supported'
                      : 'Manual amount entry',
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

class _ParamField extends StatelessWidget {
  const _ParamField({
    required this.param,
    required this.controller,
    required this.isPrimary,
  });

  final CustomerParam param;
  final TextEditingController controller;
  final bool isPrimary;

  String? _validate(String? v) {
    final value = (v ?? '').trim();
    if (value.isEmpty) {
      return '${param.name} is required';
    }
    if (param.regex.isNotEmpty) {
      try {
        final re = RegExp(param.regex);
        if (!re.hasMatch(value)) {
          return 'Invalid ${param.name.toLowerCase()}';
        }
      } catch (_) {
        // Bad regex from server — skip client validation.
      }
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          param.name + (isPrimary ? '' : ' (optional)'),
          style: AppTextStyles.label.copyWith(color: AppColors.textPrimary),
        ),
        const SizedBox(height: AppSpacing.s),
        TextFormField(
          controller: controller,
          autovalidateMode: AutovalidateMode.onUserInteraction,
          validator: isPrimary ? _validate : null,
          style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
          decoration: InputDecoration(
            hintText: 'Enter ${param.name.toLowerCase()}',
            hintStyle: AppTextStyles.bodySmall,
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
        ),
      ],
    );
  }
}
