// A13 anomaly step-up screen for mobile. The server flagged this
// sign-in as high-risk (new /24 + new device) and refused to mint
// tokens. The user proves they're really themselves via an email OTP
// or a TOTP code from their authenticator app, then we exchange the
// pending_token for a real session via /v1/auth/anomaly/verify-email
// or /v1/auth/anomaly/verify-2fa.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class AnomalyStepUpScreen extends ConsumerStatefulWidget {
  final String pendingToken;
  final List<String> methods; // 'email_otp' / 'totp'

  const AnomalyStepUpScreen({
    super.key,
    required this.pendingToken,
    required this.methods,
  });

  @override
  ConsumerState<AnomalyStepUpScreen> createState() =>
      _AnomalyStepUpScreenState();
}

class _AnomalyStepUpScreenState extends ConsumerState<AnomalyStepUpScreen> {
  final TextEditingController _codeCtl = TextEditingController();
  final FocusNode _focus = FocusNode();
  bool _loading = false;
  String? _error;
  late String _method; // 'email_otp' or 'totp'

  static const _emailPath = '/v1/auth/anomaly/verify-email';
  static const _totpPath = '/v1/auth/anomaly/verify-2fa';

  @override
  void initState() {
    super.initState();
    // Prefer email-OTP when both methods are present (lower friction);
    // otherwise lock to whatever the server allows.
    _method = widget.methods.contains('email_otp')
        ? 'email_otp'
        : widget.methods.contains('totp')
            ? 'totp'
            : 'email_otp';
    WidgetsBinding.instance.addPostFrameCallback((_) => _focus.requestFocus());
  }

  @override
  void dispose() {
    _codeCtl.dispose();
    _focus.dispose();
    super.dispose();
  }

  Future<void> _verify(String code) async {
    if (_loading || code.length < 6) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    final auth = ref.read(authServiceProvider);
    final ok = await auth.completeStepUp(
      path: _method == 'email_otp' ? _emailPath : _totpPath,
      pendingToken: widget.pendingToken,
      code: code,
    );
    if (!mounted) return;
    if (ok) {
      context.go('/');
      return;
    }
    setState(() {
      _loading = false;
      _error = 'Verification failed. Check the code and try again.';
      _codeCtl.clear();
    });
  }

  void _switchMethod() {
    if (widget.methods.length < 2) return;
    setState(() {
      _method = _method == 'email_otp' ? 'totp' : 'email_otp';
      _codeCtl.clear();
      _error = null;
    });
    _focus.requestFocus();
  }

  @override
  Widget build(BuildContext context) {
    final description = _method == 'email_otp'
        ? 'Enter the 6-digit code we just sent to your registered email.'
        : 'Enter the 6-digit code from your authenticator app.';

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: Colors.transparent,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textSecondary, size: 20),
          onPressed: () => context.go('/login'),
        ),
      ),
      body: SafeArea(
        child: SingleChildScrollView(
          padding: AppSpacing.pagePadding.copyWith(top: 8, bottom: 32),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                width: 56,
                height: 56,
                decoration: BoxDecoration(
                  color: Colors.amber.withValues(alpha: 0.12),
                  borderRadius: BorderRadius.circular(16),
                ),
                child: const Icon(Icons.shield_outlined,
                    color: Colors.amber, size: 28),
              ),
              const SizedBox(height: 16),
              Text('Verify it\'s you', style: AppTextStyles.h2),
              const SizedBox(height: 4),
              Text(
                'Unfamiliar sign-in',
                style: AppTextStyles.labelTiny
                    .copyWith(color: Colors.amber, letterSpacing: 1.2),
              ),
              const SizedBox(height: 16),
              Text(
                'We noticed this sign-in is from a new device on a new '
                'network. $description',
                style: AppTextStyles.body
                    .copyWith(color: AppColors.textSecondary),
              ),
              const SizedBox(height: 24),
              if (_error != null) ...[
                Container(
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: Colors.red.withValues(alpha: 0.08),
                    border: Border.all(color: Colors.red.withValues(alpha: 0.3)),
                    borderRadius: BorderRadius.circular(12),
                  ),
                  child: Text(
                    _error!,
                    style: AppTextStyles.body.copyWith(color: Colors.red),
                  ),
                ),
                const SizedBox(height: 16),
              ],
              TextField(
                controller: _codeCtl,
                focusNode: _focus,
                keyboardType: TextInputType.number,
                textAlign: TextAlign.center,
                inputFormatters: [
                  FilteringTextInputFormatter.digitsOnly,
                  LengthLimitingTextInputFormatter(6),
                ],
                style: AppTextStyles.h2.copyWith(letterSpacing: 8),
                decoration: InputDecoration(
                  hintText: '000000',
                  filled: true,
                  fillColor: AppColors.bgCard,
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(12),
                    borderSide: BorderSide(color: AppColors.borderSubtle),
                  ),
                ),
                onChanged: (v) {
                  if (v.length == 6) _verify(v);
                },
                onSubmitted: _verify,
              ),
              const SizedBox(height: 16),
              SizedBox(
                width: double.infinity,
                child: ElevatedButton(
                  onPressed:
                      _loading ? null : () => _verify(_codeCtl.text.trim()),
                  child: _loading
                      ? const SizedBox(
                          height: 18,
                          width: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Text('Verify'),
                ),
              ),
              if (widget.methods.length > 1) ...[
                const SizedBox(height: 12),
                Center(
                  child: TextButton(
                    onPressed: _switchMethod,
                    child: Text(_method == 'email_otp'
                        ? 'Use authenticator app instead'
                        : 'Use email code instead'),
                  ),
                ),
              ],
              const SizedBox(height: 24),
              Text(
                'If this wasn\'t you, change your password immediately '
                'and review recent activity from your account settings.',
                style: AppTextStyles.labelTiny
                    .copyWith(color: AppColors.textTertiary, height: 1.5),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
