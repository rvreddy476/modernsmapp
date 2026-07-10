import 'package:atpost_design/app_colors.dart';
import 'package:atpost_design/app_spacing.dart';
import 'package:atpost_design/app_text_styles.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Final step of the forgot-password flow: the user arrived here from
/// the OTP screen with a verified-looking [code]; the server actually
/// validates identifier+code when the new password is submitted
/// (POST /reset-password — there is no separate code-check endpoint).
class ResetPasswordScreen extends ConsumerStatefulWidget {
  final String identifier;

  /// The one-time reset code, handed over via the router's `extra`
  /// (never the URL) so it stays out of navigation logs.
  final String code;

  const ResetPasswordScreen({
    super.key,
    required this.identifier,
    required this.code,
  });

  @override
  ConsumerState<ResetPasswordScreen> createState() =>
      _ResetPasswordScreenState();
}

class _ResetPasswordScreenState extends ConsumerState<ResetPasswordScreen> {
  final _passwordController = TextEditingController();
  final _confirmController = TextEditingController();

  bool _loading = false;
  bool _obscurePassword = true;
  bool _obscureConfirm = true;

  @override
  void dispose() {
    _passwordController.dispose();
    _confirmController.dispose();
    super.dispose();
  }

  String? _validate() {
    final password = _passwordController.text;
    if (password.isEmpty) return 'Please enter a new password.';
    if (password.length < 8) return 'Password must be at least 8 characters.';
    if (password != _confirmController.text) return 'Passwords do not match.';
    return null;
  }

  Future<void> _submit() async {
    if (widget.code.isEmpty) {
      // Reached without a code (deep link / restored route) — the reset
      // session is gone, restart the flow.
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
            content: Text('This reset session expired. Please start over.')),
      );
      context.go('/forgot-password');
      return;
    }

    final validationError = _validate();
    if (validationError != null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(validationError)),
      );
      return;
    }

    setState(() => _loading = true);

    final error = await ref.read(authServiceProvider).resetPassword(
          identifier: widget.identifier,
          code: widget.code,
          newPassword: _passwordController.text,
        );

    if (!mounted) return;
    setState(() => _loading = false);

    if (error == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
            content: Text('Password reset successfully. Please log in.')),
      );
      context.go('/login');
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
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textSecondary, size: 20),
          onPressed: () => context.pop(),
        ),
        title: Text('New Password', style: AppTextStyles.h2),
        centerTitle: true,
      ),
      body: SafeArea(
        child: SingleChildScrollView(
          padding: AppSpacing.pagePadding.copyWith(top: 24, bottom: 32),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const SizedBox(height: 24),
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
                    Icons.password_rounded,
                    color: AppColors.postbookPrimary,
                    size: 34,
                  ),
                ),
              ),
              const SizedBox(height: 28),
              Text(
                'Set a new password',
                style: AppTextStyles.h1,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 10),
              Text(
                'Choose a strong password for\n${widget.identifier}',
                style:
                    AppTextStyles.body.copyWith(color: AppColors.textTertiary),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 36),
              _buildLabel('New password'),
              const SizedBox(height: 6),
              _buildPasswordField(
                controller: _passwordController,
                hint: 'At least 8 characters',
                obscured: _obscurePassword,
                onToggle: () =>
                    setState(() => _obscurePassword = !_obscurePassword),
                textInputAction: TextInputAction.next,
              ),
              const SizedBox(height: 18),
              _buildLabel('Confirm password'),
              const SizedBox(height: 6),
              _buildPasswordField(
                controller: _confirmController,
                hint: 'Repeat your new password',
                obscured: _obscureConfirm,
                onToggle: () =>
                    setState(() => _obscureConfirm = !_obscureConfirm),
                textInputAction: TextInputAction.done,
                onSubmitted: (_) => _submit(),
              ),
              const SizedBox(height: 32),
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
                    onTap: _loading ? null : _submit,
                    borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
                    child: Center(
                      child: _loading
                          ? const SizedBox(
                              width: 22,
                              height: 22,
                              child: CircularProgressIndicator(
                                strokeWidth: 2.5,
                                valueColor: AlwaysStoppedAnimation<Color>(
                                    Colors.white),
                              ),
                            )
                          : Text(
                              'Reset Password',
                              style: AppTextStyles.h3.copyWith(
                                color: Colors.white,
                                letterSpacing: 0.4,
                              ),
                            ),
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildLabel(String text) {
    return Text(
      text,
      style: AppTextStyles.label.copyWith(color: AppColors.textSecondary),
    );
  }

  Widget _buildPasswordField({
    required TextEditingController controller,
    required String hint,
    required bool obscured,
    required VoidCallback onToggle,
    TextInputAction textInputAction = TextInputAction.next,
    void Function(String)? onSubmitted,
  }) {
    return TextField(
      controller: controller,
      obscureText: obscured,
      textInputAction: textInputAction,
      onSubmitted: onSubmitted,
      style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
      decoration: InputDecoration(
        hintText: hint,
        hintStyle: AppTextStyles.body.copyWith(color: AppColors.textDim),
        filled: true,
        fillColor: AppColors.bgCard,
        contentPadding:
            const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        suffixIcon: IconButton(
          icon: Icon(
            obscured ? Icons.visibility_off_outlined : Icons.visibility_outlined,
            color: AppColors.textMuted,
            size: 20,
          ),
          onPressed: onToggle,
        ),
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
          borderSide:
              const BorderSide(color: AppColors.postbookPrimary, width: 1.5),
        ),
      ),
    );
  }
}
