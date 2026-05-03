import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:webview_flutter/webview_flutter.dart';

/// DPDP-aware copy reused across every Aadhaar surface. Source of truth so
/// the disclosure text never drifts between screens.
const String kAadhaarDpdpDisclosure =
    'We use DigiLocker. We never store your Aadhaar number. '
    'Only a verification token + the document type. You can revoke anytime.';

enum _AadhaarStatus { idle, starting, inProgress, success, failure }

/// Sprint 4 — Aadhaar/DigiLocker verification UI.
///
/// Flow:
///   1. User taps "Continue with DigiLocker".
///   2. We call `pulseRepository.startAadhaarVerification()` to mint state +
///      authorize URL.
///   3. We open the URL in an embedded WebView. When DigiLocker redirects
///      back to `/pulse/verification/aadhaar/callback?code=&state=`, the
///      router intercepts the deep link.
///   4. The callback route calls back into this screen via the `incomingCode`
///      / `incomingState` constructor args (or via a fresh navigation) and we
///      hit `completeAadhaarVerification(code, state)`.
class AadhaarVerificationScreen extends ConsumerStatefulWidget {
  const AadhaarVerificationScreen({
    super.key,
    this.incomingCode,
    this.incomingState,
  });

  /// When non-null, the screen runs the callback path immediately on mount.
  /// Set by the router for `/pulse/verification/aadhaar/callback`.
  final String? incomingCode;
  final String? incomingState;

  @override
  ConsumerState<AadhaarVerificationScreen> createState() =>
      _AadhaarVerificationScreenState();
}

class _AadhaarVerificationScreenState
    extends ConsumerState<AadhaarVerificationScreen> {
  _AadhaarStatus _status = _AadhaarStatus.idle;
  String? _errorMessage;
  AadhaarFlowStart? _flow;
  AadhaarFlowResult? _result;
  WebViewController? _webController;

  @override
  void initState() {
    super.initState();
    if (widget.incomingCode != null && widget.incomingState != null) {
      _status = _AadhaarStatus.inProgress;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        _completeFlow(widget.incomingCode!, widget.incomingState!);
      });
    }
  }

  Future<void> _start() async {
    setState(() {
      _status = _AadhaarStatus.starting;
      _errorMessage = null;
    });
    try {
      final repo = ref.read(pulseRepositoryProvider);
      final flow = await repo.startAadhaarVerification();
      if (!mounted) return;
      if (flow.authorizeUrl.isEmpty) {
        setState(() {
          _status = _AadhaarStatus.failure;
          _errorMessage = 'DigiLocker is unreachable right now.';
        });
        return;
      }
      final controller = WebViewController()
        ..setJavaScriptMode(JavaScriptMode.unrestricted)
        ..setNavigationDelegate(NavigationDelegate(
          onNavigationRequest: (req) {
            // Intercept the redirect URI and finish the flow without ever
            // letting the WebView render the app's own callback page.
            final uri = Uri.tryParse(req.url);
            if (uri != null &&
                uri.path.contains('/pulse/verification/aadhaar/callback')) {
              final code = uri.queryParameters['code'];
              final state = uri.queryParameters['state'];
              if (code != null && state != null) {
                _completeFlow(code, state);
                return NavigationDecision.prevent;
              }
            }
            return NavigationDecision.navigate;
          },
        ))
        ..loadRequest(Uri.parse(flow.authorizeUrl));
      setState(() {
        _flow = flow;
        _webController = controller;
        _status = _AadhaarStatus.inProgress;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _status = _AadhaarStatus.failure;
        _errorMessage = 'Could not start DigiLocker. Please try again.';
      });
    }
  }

  Future<void> _completeFlow(String code, String state) async {
    setState(() {
      _status = _AadhaarStatus.inProgress;
      _errorMessage = null;
    });
    try {
      final repo = ref.read(pulseRepositoryProvider);
      final result = await repo.completeAadhaarVerification(
        code: code,
        state: state,
      );
      if (!mounted) return;
      setState(() {
        _result = result;
        _status =
            result.success ? _AadhaarStatus.success : _AadhaarStatus.failure;
        _errorMessage = result.errorMessage;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _status = _AadhaarStatus.failure;
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
        title: Text('Verify with Aadhaar', style: AppTextStyles.h2),
        leading: IconButton(
          onPressed: () => context.pop(),
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
        ),
      ),
      body: SafeArea(
        child: _status == _AadhaarStatus.inProgress && _webController != null
            ? WebViewWidget(controller: _webController!)
            : SingleChildScrollView(
                padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 24),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    _TierExplainer(),
                    const SizedBox(height: 16),
                    const _DpdpDisclosureCard(),
                    const SizedBox(height: 16),
                    if (_status == _AadhaarStatus.success)
                      _SuccessCard(result: _result),
                    if (_status == _AadhaarStatus.failure)
                      _FailureCard(message: _errorMessage),
                    const SizedBox(height: 24),
                    _PrimaryCta(
                      status: _status,
                      onTap: _status == _AadhaarStatus.starting ||
                              _status == _AadhaarStatus.inProgress
                          ? null
                          : _start,
                    ),
                    const SizedBox(height: 12),
                    Text(
                      'You will be redirected to DigiLocker to authorise '
                      'sharing your Aadhaar verification token. We do not see '
                      'your Aadhaar number at any point.',
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

class _TierExplainer extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
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
              Text('Aadhaar Verified unlocks',
                  style: AppTextStyles.h3),
            ],
          ),
          const SizedBox(height: 10),
          Text('• Verified ID badge on your profile',
              style: AppTextStyles.bodySmall),
          const SizedBox(height: 4),
          Text('• Safer-meet check-in flow', style: AppTextStyles.bodySmall),
          const SizedBox(height: 4),
          Text('• Full Pulse pool when your intent is Marriage',
              style: AppTextStyles.bodySmall),
        ],
      ),
    );
  }
}

class _DpdpDisclosureCard extends StatelessWidget {
  const _DpdpDisclosureCard();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
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
                Text(kAadhaarDpdpDisclosure, style: AppTextStyles.bodySmall),
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

  final _AadhaarStatus status;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    String label;
    switch (status) {
      case _AadhaarStatus.starting:
        label = 'Starting…';
        break;
      case _AadhaarStatus.inProgress:
        label = 'Continuing in DigiLocker…';
        break;
      case _AadhaarStatus.success:
        label = 'Verified ✓';
        break;
      case _AadhaarStatus.failure:
        label = 'Try again';
        break;
      case _AadhaarStatus.idle:
        label = 'Continue with DigiLocker';
    }
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
        child: Text(label,
            style: AppTextStyles.h3.copyWith(color: Colors.white)),
      ),
    );
  }
}

class _SuccessCard extends StatelessWidget {
  const _SuccessCard({required this.result});

  final AadhaarFlowResult? result;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.statusSuccess.withAlpha(36),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.statusSuccess.withAlpha(80)),
      ),
      child: Row(
        children: [
          const Icon(Icons.check_circle,
              color: AppColors.statusSuccess, size: 28),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Aadhaar Verified ✓',
                    style: AppTextStyles.h3.copyWith(
                        color: AppColors.statusSuccess)),
                const SizedBox(height: 4),
                Text(
                  result?.verifiedAt != null
                      ? 'Trust tier upgraded to ${result!.trustTier}.'
                      : 'Trust tier upgraded.',
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
      padding: const EdgeInsets.all(16),
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
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('We could not verify',
                    style: AppTextStyles.h3.copyWith(
                        color: AppColors.statusError)),
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
