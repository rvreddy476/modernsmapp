import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ForgotPasswordScreen extends ConsumerStatefulWidget {
  const ForgotPasswordScreen({super.key});

  @override
  ConsumerState<ForgotPasswordScreen> createState() => _ForgotPasswordScreenState();
}

class _ForgotPasswordScreenState extends ConsumerState<ForgotPasswordScreen> {
  final _identifierController = TextEditingController();
  bool _loading = false;

  @override
  void dispose() {
    _identifierController.dispose();
    super.dispose();
  }

  Future<void> _sendResetCode() async {
    final identifier = _identifierController.text.trim();
    if (identifier.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Please enter your email or phone number.')),
      );
      return;
    }

    setState(() => _loading = true);

    try {
      final dio = Dio(BaseOptions(
        baseUrl: Environment.apiBaseUrl,
        connectTimeout: const Duration(seconds: 10),
      ));

      await dio.post(
        '${Environment.authPath}/request-otp',
        data: {'identifier': identifier},
      );

      if (!mounted) return;

      context.push('/verify-otp?id=${Uri.encodeComponent(identifier)}&mode=reset');
    } on DioException catch (e) {
      if (!mounted) return;
      final body = e.response?.data;
      final rawErr = body is Map ? body['error'] : null;
      final message = rawErr is Map
          ? (rawErr['message'] as String? ?? rawErr['code'] as String?)
          : rawErr is String
              ? rawErr
              : (body is Map ? body['message'] as String? : null);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(message ?? 'Failed to send reset code. Please try again.'),
        ),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Failed to send reset code. Please try again.')),
      );
    } finally {
      if (mounted) setState(() => _loading = false);
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
        title: Text('Reset Password', style: AppTextStyles.h2),
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
                    Icons.lock_reset_rounded,
                    color: AppColors.postbookPrimary,
                    size: 34,
                  ),
                ),
              ),

              const SizedBox(height: 28),

              Text(
                'Forgot your password?',
                style: AppTextStyles.h1,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 10),
              Text(
                "Enter your email or phone number and we'll send you a reset code.",
                style: AppTextStyles.body.copyWith(color: AppColors.textTertiary),
                textAlign: TextAlign.center,
              ),

              const SizedBox(height: 36),

              // Identifier field
              Text(
                'Email or phone number',
                style: AppTextStyles.label.copyWith(color: AppColors.textSecondary),
              ),
              const SizedBox(height: 6),
              TextField(
                controller: _identifierController,
                keyboardType: TextInputType.emailAddress,
                textInputAction: TextInputAction.done,
                onSubmitted: (_) => _sendResetCode(),
                style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
                decoration: InputDecoration(
                  hintText: 'you@example.com or +1234567890',
                  hintStyle: AppTextStyles.body.copyWith(color: AppColors.textDim),
                  filled: true,
                  fillColor: AppColors.bgCard,
                  contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                    borderSide: BorderSide(color: AppColors.borderSubtle),
                  ),
                  enabledBorder: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                    borderSide: BorderSide(color: AppColors.borderSubtle),
                  ),
                  focusedBorder: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                    borderSide: const BorderSide(color: AppColors.postbookPrimary, width: 1.5),
                  ),
                ),
              ),

              const SizedBox(height: 32),

              // Send reset code button
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
                    onTap: _loading ? null : _sendResetCode,
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
                              'Send Reset Code',
                              style: AppTextStyles.h3.copyWith(color: Colors.white, letterSpacing: 0.4),
                            ),
                    ),
                  ),
                ),
              ),

              const SizedBox(height: 24),

              // Back to login
              Center(
                child: TextButton(
                  onPressed: () => context.pop(),
                  style: TextButton.styleFrom(
                    foregroundColor: AppColors.textTertiary,
                  ),
                  child: Text(
                    'Back to Login',
                    style: AppTextStyles.bodySmall.copyWith(color: AppColors.textTertiary),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
