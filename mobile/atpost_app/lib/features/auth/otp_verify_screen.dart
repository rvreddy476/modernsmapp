import 'dart:async';

import 'package:atpost_design/app_colors.dart';
import 'package:atpost_design/app_spacing.dart';
import 'package:atpost_design/app_text_styles.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class OtpVerifyScreen extends ConsumerStatefulWidget {
  final String identifier;
  final String mode; // 'login' | 'reset' | '2fa'

  /// One-shot server token gating the 2FA exchange. Passed via the
  /// router's `extra` (never the URL) so it can't leak through
  /// navigation logs or deep-link history.
  final String? pendingToken;

  const OtpVerifyScreen({
    super.key,
    required this.identifier,
    required this.mode,
    this.pendingToken,
  });

  @override
  ConsumerState<OtpVerifyScreen> createState() => _OtpVerifyScreenState();
}

class _OtpVerifyScreenState extends ConsumerState<OtpVerifyScreen> {
  static const int _otpLength = 6;

  final List<TextEditingController> _controllers =
      List.generate(_otpLength, (_) => TextEditingController());
  final List<FocusNode> _focusNodes =
      List.generate(_otpLength, (_) => FocusNode());

  bool _loading = false;

  // Resend countdown
  int _resendCountdown = 60;
  Timer? _resendTimer;

  @override
  void initState() {
    super.initState();
    _startResendTimer();
    // Auto-focus the first field
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _focusNodes[0].requestFocus();
    });
  }

  @override
  void dispose() {
    for (final c in _controllers) {
      c.dispose();
    }
    for (final f in _focusNodes) {
      f.dispose();
    }
    _resendTimer?.cancel();
    super.dispose();
  }

  void _startResendTimer() {
    _resendTimer?.cancel();
    setState(() => _resendCountdown = 60);
    _resendTimer = Timer.periodic(const Duration(seconds: 1), (timer) {
      if (_resendCountdown <= 1) {
        timer.cancel();
        if (mounted) setState(() => _resendCountdown = 0);
      } else {
        if (mounted) setState(() => _resendCountdown--);
      }
    });
  }

  String get _otp => _controllers.map((c) => c.text).join();

  void _onDigitEntered(int index, String value) {
    if (value.isEmpty) {
      // Backspace — move to previous field
      if (index > 0) {
        _focusNodes[index - 1].requestFocus();
        _controllers[index - 1].clear();
      }
      return;
    }

    // Accept only last character if multiple pasted
    final digit = value.characters.last;
    _controllers[index].text = digit;
    _controllers[index].selection = TextSelection.fromPosition(
      TextPosition(offset: 1),
    );

    if (index < _otpLength - 1) {
      _focusNodes[index + 1].requestFocus();
    } else {
      // Last field — dismiss keyboard and auto-verify
      _focusNodes[index].unfocus();
      _verify();
    }
  }

  Future<void> _verify() async {
    final otp = _otp;
    if (otp.length < _otpLength) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Please enter the complete 6-digit code.')),
      );
      return;
    }

    setState(() => _loading = true);

    // Verification runs through AuthService's pinned + CSRF-aware client
    // and mints the session there; this screen only renders the outcome.
    final error = await ref.read(authServiceProvider).verifyOtp(
          identifier: widget.identifier,
          code: otp,
          pendingToken: widget.pendingToken,
          is2fa: widget.mode == '2fa',
        );

    if (!mounted) return;
    setState(() => _loading = false);

    if (error == null) {
      context.go('/');
      return;
    }
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(error)),
    );
    _clearOtp();
  }

  void _clearOtp() {
    for (final c in _controllers) {
      c.clear();
    }
    _focusNodes[0].requestFocus();
  }

  Future<void> _resendCode() async {
    if (_resendCountdown > 0) return;

    final error =
        await ref.read(authServiceProvider).requestOtp(widget.identifier);
    if (!mounted) return;

    if (error == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Code resent successfully.')),
      );
      _startResendTimer();
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(error)),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: Colors.transparent,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new_rounded, color: AppColors.textSecondary, size: 20),
          onPressed: () => context.pop(),
        ),
        title: Text('Verify Code', style: AppTextStyles.h2),
        centerTitle: true,
      ),
      body: SafeArea(
        child: SingleChildScrollView(
          padding: AppSpacing.pagePadding.copyWith(top: 24, bottom: 32),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const SizedBox(height: 24),

              // Icon
              Center(
                child: Container(
                  width: 72,
                  height: 72,
                  decoration: BoxDecoration(
                    color: AppColors.bgCard,
                    borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                    border: Border.all(color: AppColors.borderSubtle),
                  ),
                  child: const Icon(
                    Icons.sms_outlined,
                    color: AppColors.postbookPrimary,
                    size: 34,
                  ),
                ),
              ),

              const SizedBox(height: 28),

              Text(
                'Enter verification code',
                style: AppTextStyles.h1,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 10),
              Text(
                'We sent a 6-digit code to\n${widget.identifier}',
                style: AppTextStyles.body.copyWith(color: AppColors.textTertiary),
                textAlign: TextAlign.center,
              ),

              const SizedBox(height: 40),

              // OTP digit fields
              Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: List.generate(_otpLength, (index) {
                  return Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 5),
                    child: SizedBox(
                      width: 44,
                      height: 52,
                      child: TextField(
                        controller: _controllers[index],
                        focusNode: _focusNodes[index],
                        textAlign: TextAlign.center,
                        keyboardType: TextInputType.number,
                        inputFormatters: [
                          FilteringTextInputFormatter.digitsOnly,
                          LengthLimitingTextInputFormatter(2),
                        ],
                        style: AppTextStyles.h2.copyWith(
                          color: AppColors.textPrimary,
                          fontSize: 20,
                        ),
                        onChanged: (value) => _onDigitEntered(index, value),
                        decoration: InputDecoration(
                          counterText: '',
                          filled: true,
                          fillColor: AppColors.bgCard,
                          contentPadding: EdgeInsets.zero,
                          border: OutlineInputBorder(
                            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                            borderSide: BorderSide(color: AppColors.borderSubtle),
                          ),
                          enabledBorder: OutlineInputBorder(
                            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                            borderSide: BorderSide(color: AppColors.borderSubtle),
                          ),
                          focusedBorder: OutlineInputBorder(
                            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                            borderSide: const BorderSide(
                              color: AppColors.postbookPrimary,
                              width: 1.5,
                            ),
                          ),
                        ),
                      ),
                    ),
                  );
                }),
              ),

              const SizedBox(height: 40),

              // Verify button
              Container(
                height: 52,
                decoration: BoxDecoration(
                  gradient: _loading ? null : AppColors.postbookGradient,
                  color: _loading ? AppColors.textDim : null,
                  borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
                ),
                child: Material(
                  color: Colors.transparent,
                  child: InkWell(
                    onTap: _loading ? null : _verify,
                    borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
                    child: Center(
                      child: _loading
                          ? const SizedBox(
                              width: 22,
                              height: 22,
                              child: CircularProgressIndicator(
                                strokeWidth: 2.5,
                                valueColor: AlwaysStoppedAnimation<Color>(Colors.white),
                              ),
                            )
                          : Text(
                              'Verify',
                              style: AppTextStyles.h3.copyWith(
                                color: Colors.white,
                                letterSpacing: 0.4,
                              ),
                            ),
                    ),
                  ),
                ),
              ),

              const SizedBox(height: 28),

              // Resend row
              Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Text(
                    "Didn't receive it? ",
                    style: AppTextStyles.bodySmall.copyWith(color: AppColors.textTertiary),
                  ),
                  _resendCountdown > 0
                      ? Text(
                          'Resend in ${_resendCountdown}s',
                          style: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
                        )
                      : TextButton(
                          onPressed: _resendCode,
                          style: TextButton.styleFrom(
                            foregroundColor: AppColors.postbookPrimary,
                            padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 4),
                            tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                          ),
                          child: Text(
                            'Resend Code',
                            style: AppTextStyles.bodySmall.copyWith(
                              color: AppColors.postbookPrimary,
                              fontWeight: FontWeight.w700,
                            ),
                          ),
                        ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}
