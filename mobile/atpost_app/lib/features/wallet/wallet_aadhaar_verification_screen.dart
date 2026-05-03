// Wallet Aadhaar verification — Phase 2 Sprint 1.
//
// Mirrors the Pulse Aadhaar flow exactly so DPDP copy stays consistent
// across surfaces. The disclosure text is verbatim from the Pulse screen
// per the PRD: "We use DigiLocker. We never store your Aadhaar number.
// Only a verification token."
//
// Flow:
//   1. Tap "Continue with DigiLocker".
//   2. POST `/v1/wallet/kyc/aadhaar/start` → mints `digilocker_authorize_url`.
//   3. Open the URL in an embedded WebView.
//   4. DigiLocker redirects back to `/wallet/kyc/aadhaar/callback?code=&state=`.
//   5. POST `/v1/wallet/kyc/aadhaar/callback` with the code + state → KYC tier
//      upgrades server-side; we invalidate `walletKYCProvider` and
//      `walletBalanceProvider` so the home re-fetches.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/wallet.dart';
import 'package:atpost_app/data/repositories/wallet_repository.dart';
import 'package:atpost_app/providers/wallet_providers.dart';
import 'package:atpost_app/services/wallet_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:webview_flutter/webview_flutter.dart';

/// DPDP-aware copy. Same wording as Pulse so users see identical assurance
/// across the app.
const String kWalletAadhaarDpdpDisclosure =
    'We use DigiLocker. We never store your Aadhaar number. '
    'Only a verification token.';

enum _Status { idle, starting, inProgress, success, failure }

class WalletAadhaarVerificationScreen extends ConsumerStatefulWidget {
  const WalletAadhaarVerificationScreen({
    super.key,
    this.incomingCode,
    this.incomingState,
  });

  final String? incomingCode;
  final String? incomingState;

  @override
  ConsumerState<WalletAadhaarVerificationScreen> createState() =>
      _WalletAadhaarVerificationScreenState();
}

class _WalletAadhaarVerificationScreenState
    extends ConsumerState<WalletAadhaarVerificationScreen> {
  _Status _status = _Status.idle;
  String? _errorMessage;
  WalletAadhaarStart? _start;
  WalletKYCState? _result;
  WebViewController? _webController;

  @override
  void initState() {
    super.initState();
    if (widget.incomingCode != null && widget.incomingState != null) {
      _status = _Status.inProgress;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        _completeFlow(widget.incomingCode!, widget.incomingState!);
      });
    }
  }

  Future<void> _begin() async {
    setState(() {
      _status = _Status.starting;
      _errorMessage = null;
    });
    final tier = ref.read(walletKYCProvider).asData?.value.tier ?? 'minimal';
    ref.read(walletTelemetryProvider).walletKYCStarted(tier: tier);
    try {
      final repo = ref.read(walletRepositoryProvider);
      final start = await repo.startAadhaarKYC();
      if (!mounted) return;
      if (start.digilockerAuthorizeUrl.isEmpty) {
        setState(() {
          _status = _Status.failure;
          _errorMessage = 'DigiLocker is unreachable right now.';
        });
        return;
      }
      final controller = WebViewController()
        ..setJavaScriptMode(JavaScriptMode.unrestricted)
        ..setNavigationDelegate(
          NavigationDelegate(
            onNavigationRequest: (req) {
              final uri = Uri.tryParse(req.url);
              if (uri != null &&
                  uri.path.contains('/wallet/kyc/aadhaar/callback')) {
                final code = uri.queryParameters['code'];
                final state = uri.queryParameters['state'];
                if (code != null && state != null) {
                  _completeFlow(code, state);
                  return NavigationDecision.prevent;
                }
              }
              return NavigationDecision.navigate;
            },
          ),
        )
        ..loadRequest(Uri.parse(start.digilockerAuthorizeUrl));
      setState(() {
        _start = start;
        _webController = controller;
        _status = _Status.inProgress;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _status = _Status.failure;
        _errorMessage = 'Could not start DigiLocker. Please try again.';
      });
    }
  }

  Future<void> _completeFlow(String code, String state) async {
    setState(() {
      _status = _Status.inProgress;
      _errorMessage = null;
    });
    try {
      final repo = ref.read(walletRepositoryProvider);
      final result = await repo.completeAadhaarKYC(code: code, state: state);
      if (!mounted) return;
      ref.invalidate(walletKYCProvider);
      ref.invalidate(walletBalanceProvider);
      ref
          .read(walletTelemetryProvider)
          .walletKYCCompleted(tier: result.tier);
      setState(() {
        _result = result;
        _status = _Status.success;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _status = _Status.failure;
        _errorMessage = 'Verification failed. Please try again.';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Verify Aadhaar', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
          onPressed: () => context.pop(),
        ),
      ),
      body: SafeArea(
        child: _status == _Status.inProgress && _webController != null
            ? WebViewWidget(controller: _webController!)
            : SingleChildScrollView(
                padding: const EdgeInsets.all(AppSpacing.l),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    const _Explainer(),
                    const SizedBox(height: AppSpacing.l),
                    const _DisclosureCard(),
                    const SizedBox(height: AppSpacing.l),
                    if (_status == _Status.success && _result != null)
                      _SuccessCard(result: _result!),
                    if (_status == _Status.failure)
                      _FailureCard(message: _errorMessage),
                    const SizedBox(height: AppSpacing.l),
                    _PrimaryCta(
                      status: _status,
                      onTap: _status == _Status.starting ||
                              _status == _Status.inProgress
                          ? null
                          : _begin,
                    ),
                    const SizedBox(height: AppSpacing.s),
                    Text(
                      'You will be redirected to DigiLocker to authorise '
                      'sharing your Aadhaar verification token. We do not see '
                      'your Aadhaar number.',
                      style: AppTextStyles.bodySmall,
                      textAlign: TextAlign.center,
                    ),
                  ],
                ),
              ),
      ),
    );
  }
}

class _Explainer extends StatelessWidget {
  const _Explainer();

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
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(Icons.verified_user,
                  color: AppColors.posttubePrimary, size: 22),
              const SizedBox(width: 8),
              Text('Aadhaar Verified unlocks', style: AppTextStyles.h3),
            ],
          ),
          const SizedBox(height: 10),
          Text('• ₹2L monthly wallet limit', style: AppTextStyles.bodySmall),
          const SizedBox(height: 4),
          Text('• Send up to ₹50,000 per transaction',
              style: AppTextStyles.bodySmall),
          const SizedBox(height: 4),
          Text('• Verified ID badge on your AtPost profile',
              style: AppTextStyles.bodySmall),
        ],
      ),
    );
  }
}

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
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Your data, under DPDP', style: AppTextStyles.h3),
                const SizedBox(height: 6),
                Text(
                  kWalletAadhaarDpdpDisclosure,
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

class _PrimaryCta extends StatelessWidget {
  const _PrimaryCta({required this.status, required this.onTap});

  final _Status status;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final label = switch (status) {
      _Status.starting => 'Starting…',
      _Status.inProgress => 'Continuing in DigiLocker…',
      _Status.success => 'Verified',
      _Status.failure => 'Try again',
      _Status.idle => 'Continue with DigiLocker',
    };
    return SizedBox(
      height: 52,
      child: FilledButton(
        onPressed: onTap,
        style: FilledButton.styleFrom(
          backgroundColor: AppColors.postbookPrimary,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.h3.copyWith(color: Colors.white),
        ),
      ),
    );
  }
}

class _SuccessCard extends StatelessWidget {
  const _SuccessCard({required this.result});

  final WalletKYCState result;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.statusSuccess.withAlpha(36),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.statusSuccess.withAlpha(80)),
      ),
      child: Row(
        children: [
          const Icon(Icons.check_circle,
              color: AppColors.statusSuccess, size: 28),
          const SizedBox(width: AppSpacing.l),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Aadhaar Verified',
                  style: AppTextStyles.h3.copyWith(
                    color: AppColors.statusSuccess,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  'KYC tier upgraded to ${result.tier}.',
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

class _FailureCard extends StatelessWidget {
  const _FailureCard({required this.message});

  final String? message;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.statusError.withAlpha(36),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.statusError.withAlpha(80)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.error_outline,
              color: AppColors.statusError, size: 22),
          const SizedBox(width: AppSpacing.s),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'We could not verify',
                  style: AppTextStyles.h3.copyWith(
                    color: AppColors.statusError,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  message ?? 'Something went wrong. Please retry.',
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
