// Wallet KYC — Phase 2 Sprint 1.
//
// Tier ladder: Minimal (₹10k limit) → Full (₹2L limit) → Enhanced.
// Aadhaar via DigiLocker (delegated to `WalletAadhaarVerificationScreen`)
// and PAN via masked input. Pulse-style DPDP disclosure.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/wallet.dart';
import 'package:atpost_app/data/repositories/wallet_repository.dart';
import 'package:atpost_app/providers/wallet_providers.dart';
import 'package:atpost_app/services/wallet_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class WalletKycScreen extends ConsumerStatefulWidget {
  const WalletKycScreen({super.key});

  @override
  ConsumerState<WalletKycScreen> createState() => _WalletKycScreenState();
}

class _WalletKycScreenState extends ConsumerState<WalletKycScreen> {
  final _panCtrl = TextEditingController();
  bool _submittingPan = false;
  String? _panMessage;
  bool _panError = false;

  @override
  void dispose() {
    _panCtrl.dispose();
    super.dispose();
  }

  static final _panRegex = RegExp(r'^[A-Z]{5}[0-9]{4}[A-Z]$');

  Future<void> _submitPan() async {
    final text = _panCtrl.text.trim().toUpperCase();
    if (!_panRegex.hasMatch(text)) {
      setState(() {
        _panMessage = 'Enter a valid PAN (AAAAA9999A)';
        _panError = true;
      });
      return;
    }
    setState(() {
      _submittingPan = true;
      _panMessage = null;
      _panError = false;
    });
    try {
      final repo = ref.read(walletRepositoryProvider);
      final state = await repo.submitPAN(text);
      if (!mounted) return;
      ref.invalidate(walletKYCProvider);
      ref.invalidate(walletBalanceProvider);
      setState(() {
        _submittingPan = false;
        _panError = false;
        _panMessage = state.panMasked != null
            ? 'PAN ${state.panMasked} submitted. We will verify shortly.'
            : 'PAN submitted. We will verify shortly.';
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _submittingPan = false;
        _panError = true;
        _panMessage = 'Could not submit PAN. Please retry.';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(walletKYCProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('KYC', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
          onPressed: () => context.pop(),
        ),
      ),
      body: async.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text('Could not load KYC: $e',
                style: AppTextStyles.bodySmall),
          ),
        ),
        data: (state) => SingleChildScrollView(
          padding: const EdgeInsets.all(AppSpacing.l),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _TierLadder(currentTier: state.tier),
              const SizedBox(height: AppSpacing.xxl),
              _AadhaarSection(state: state, ref: ref),
              const SizedBox(height: AppSpacing.l),
              _PanSection(
                controller: _panCtrl,
                state: state,
                submitting: _submittingPan,
                message: _panMessage,
                isError: _panError,
                onSubmit: _submitPan,
              ),
              const SizedBox(height: AppSpacing.xxl),
              const _DisclosureCard(),
            ],
          ),
        ),
      ),
    );
  }
}

// ─── Tier ladder ─────────────────────────────────────────────────────────

class _TierLadder extends StatelessWidget {
  const _TierLadder({required this.currentTier});

  final String currentTier;

  static const _tiers = [
    ('minimal', 'Minimal', '₹10k monthly limit', 'Phone-verified'),
    ('full', 'Full', '₹2L monthly limit', 'Aadhaar + PAN'),
    ('enhanced', 'Enhanced', 'Higher limits', 'Income proof + manual review'),
  ];

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text('Your KYC tier', style: AppTextStyles.h3),
          const SizedBox(height: AppSpacing.l),
          for (final (id, name, limit, sub) in _tiers)
            Padding(
              padding: const EdgeInsets.only(bottom: AppSpacing.s),
              child: Row(
                children: [
                  Container(
                    width: 28,
                    height: 28,
                    decoration: BoxDecoration(
                      shape: BoxShape.circle,
                      color: id == currentTier
                          ? AppColors.statusSuccess
                          : AppColors.bgTertiary,
                    ),
                    child: Icon(
                      id == currentTier
                          ? Icons.check
                          : Icons.lock_outline,
                      color: id == currentTier
                          ? Colors.white
                          : AppColors.textTertiary,
                      size: 16,
                    ),
                  ),
                  const SizedBox(width: AppSpacing.l),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(name, style: AppTextStyles.label),
                        Text(
                          '$limit · $sub',
                          style: AppTextStyles.bodySmall,
                        ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}

// ─── Aadhaar section ─────────────────────────────────────────────────────

class _AadhaarSection extends StatelessWidget {
  const _AadhaarSection({required this.state, required this.ref});

  final WalletKYCState state;
  final WidgetRef ref;

  @override
  Widget build(BuildContext context) {
    final verified = state.aadhaarStatus == 'verified';
    final pending = state.aadhaarStatus == 'pending';
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(Icons.verified_user_outlined,
                  color: AppColors.posttubePrimary),
              const SizedBox(width: AppSpacing.s),
              Text('Aadhaar', style: AppTextStyles.h3),
              const Spacer(),
              if (verified)
                Text(
                  'Verified',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.statusSuccess,
                  ),
                )
              else if (pending)
                Text(
                  'Pending',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.statusWarning,
                  ),
                ),
            ],
          ),
          const SizedBox(height: AppSpacing.s),
          Text(
            verified
                ? 'Aadhaar verified via DigiLocker.'
                : 'Verify via DigiLocker. We never store your Aadhaar number.',
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: AppSpacing.l),
          SizedBox(
            height: 44,
            child: FilledButton(
              onPressed: verified
                  ? null
                  : () {
                      ref
                          .read(walletTelemetryProvider)
                          .walletKYCStarted(tier: state.tier);
                      GoRouter.of(context).push('/wallet/kyc/aadhaar');
                    },
              style: FilledButton.styleFrom(
                backgroundColor: verified
                    ? AppColors.bgTertiary
                    : AppColors.postbookPrimary,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                ),
              ),
              child: Text(
                verified ? 'Already verified' : 'Verify with DigiLocker',
                style: AppTextStyles.label.copyWith(color: Colors.white),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

// ─── PAN section ─────────────────────────────────────────────────────────

class _PanSection extends StatelessWidget {
  const _PanSection({
    required this.controller,
    required this.state,
    required this.submitting,
    required this.message,
    required this.isError,
    required this.onSubmit,
  });

  final TextEditingController controller;
  final WalletKYCState state;
  final bool submitting;
  final String? message;
  final bool isError;
  final VoidCallback onSubmit;

  @override
  Widget build(BuildContext context) {
    final hasPan = state.panMasked != null && state.panMasked!.isNotEmpty;
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(Icons.credit_card_outlined,
                  color: AppColors.posttubePrimary),
              const SizedBox(width: AppSpacing.s),
              Text('PAN', style: AppTextStyles.h3),
              const Spacer(),
              if (state.panStatus == 'verified')
                Text(
                  'Verified',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.statusSuccess,
                  ),
                ),
            ],
          ),
          const SizedBox(height: AppSpacing.s),
          if (hasPan)
            Text(
              'PAN on file: ${state.panMasked}',
              style: AppTextStyles.body,
            )
          else ...[
            TextField(
              controller: controller,
              maxLength: 10,
              textCapitalization: TextCapitalization.characters,
              inputFormatters: [
                FilteringTextInputFormatter.allow(RegExp(r'[A-Za-z0-9]')),
                LengthLimitingTextInputFormatter(10),
                _UpperCaseFormatter(),
              ],
              style: AppTextStyles.body,
              decoration: InputDecoration(
                hintText: 'AAAAA9999A',
                counterText: '',
                hintStyle: AppTextStyles.body.copyWith(
                  color: AppColors.textGhost,
                ),
                filled: true,
                fillColor: AppColors.bgSecondary,
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                  borderSide:
                      const BorderSide(color: AppColors.borderSubtle),
                ),
              ),
            ),
            const SizedBox(height: AppSpacing.s),
            Text(
              'Stored masked. Only the last four digits are kept.',
              style: AppTextStyles.bodySmall,
            ),
          ],
          if (message != null) ...[
            const SizedBox(height: AppSpacing.s),
            Text(
              message!,
              style: AppTextStyles.bodySmall.copyWith(
                color: isError
                    ? AppColors.statusError
                    : AppColors.statusSuccess,
              ),
            ),
          ],
          const SizedBox(height: AppSpacing.l),
          SizedBox(
            height: 44,
            child: FilledButton(
              onPressed: hasPan || submitting ? null : onSubmit,
              style: FilledButton.styleFrom(
                backgroundColor: hasPan
                    ? AppColors.bgTertiary
                    : AppColors.postbookPrimary,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                ),
              ),
              child: Text(
                hasPan
                    ? 'Submitted'
                    : (submitting ? 'Submitting…' : 'Submit PAN'),
                style: AppTextStyles.label.copyWith(color: Colors.white),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _UpperCaseFormatter extends TextInputFormatter {
  @override
  TextEditingValue formatEditUpdate(
    TextEditingValue oldValue,
    TextEditingValue newValue,
  ) {
    return newValue.copyWith(text: newValue.text.toUpperCase());
  }
}

// ─── DPDP disclosure ─────────────────────────────────────────────────────

class _DisclosureCard extends StatelessWidget {
  const _DisclosureCard();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.posttubePrimary.withAlpha(80)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.shield_outlined,
              color: AppColors.posttubePrimary, size: 20),
          const SizedBox(width: AppSpacing.s),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Your data, under DPDP', style: AppTextStyles.h3),
                const SizedBox(height: 6),
                Text(
                  'We use DigiLocker. We never store your Aadhaar number. '
                  'Only a verification token. PAN is stored masked. You can '
                  'revoke anytime from Settings → Privacy.',
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
