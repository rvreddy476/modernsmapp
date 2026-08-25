import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/core/utils/validators.dart';
import 'package:atpost_app/core/widgets/app_toast.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/shared/widgets/v_input_field.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class LoginScreen extends ConsumerStatefulWidget {
  const LoginScreen({super.key});

  @override
  ConsumerState<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends ConsumerState<LoginScreen> {
  final _emailController = TextEditingController();
  final _passwordController = TextEditingController();

  String? _emailError;
  String? _passwordError;

  bool _loading = false;
  bool _obscurePassword = true;

  @override
  void dispose() {
    _emailController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  bool _validate() {
    setState(() {
      _emailError = Validators.required(_emailController.text, 'Email');
      _passwordError = Validators.required(_passwordController.text, 'Password');
    });
    return _emailError == null && _passwordError == null;
  }

  Future<void> _submit() async {
    if (!_validate()) return;

    setState(() => _loading = true);

    try {
      final auth = ref.read(authServiceProvider);
      final result = await auth.login(
        _emailController.text.trim(),
        _passwordController.text,
      );

      if (!mounted) return;

      if (result.success) {
        AppToast.success(context, 'Welcome back!');
        context.go('/');
      } else if (result.requiresStepUp) {
        final methods = result.stepUpMethods.join(',');
        context.push(
          '/auth/step-up?token=${Uri.encodeQueryComponent(result.pendingToken ?? '')}'
          '&methods=${Uri.encodeQueryComponent(methods)}',
        );
      } else if (result.requires2fa) {
        AppToast.error(
          context,
          'Two-factor authentication is required. Please sign in on the web for now.',
        );
      } else {
        // Map specific login failures
        final error = result.error ?? '';
        if (error.contains('password') || error.contains('credentials')) {
          setState(() => _passwordError = 'Incorrect email or password');
        } else {
          AppToast.error(context, error.isNotEmpty ? error : 'Login failed');
        }
      }
    } on DioException catch (e) {
      if (!mounted) return;
      AppToast.error(context, ErrorHandler.userMessageFor(e));
    } catch (e) {
      if (!mounted) return;
      AppToast.error(context, 'An unexpected error occurred');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: SingleChildScrollView(
          padding: AppSpacing.pagePadding.copyWith(top: 48, bottom: 32),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // Gradient logo
              Center(
                child: ShaderMask(
                  shaderCallback: (bounds) =>
                      AppColors.postbookGradient.createShader(bounds),
                  blendMode: BlendMode.srcIn,
                  child: Text(
                    'VChat',
                    style: AppTextStyles.logo.copyWith(fontSize: 42),
                  ),
                ),
              ),

              const SizedBox(height: 48),

              Text(
                'Welcome back',
                style: AppTextStyles.h1,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 8),
              Text(
                'Sign in to continue to your account',
                style: AppTextStyles.body.copyWith(color: AppColors.textTertiary),
                textAlign: TextAlign.center,
              ),

              const SizedBox(height: 48),

              // Email field
              VInputField(
                label: 'Email or phone',
                hint: 'you@example.com',
                controller: _emailController,
                errorText: _emailError,
                keyboardType: TextInputType.emailAddress,
                textInputAction: TextInputAction.next,
                onChanged: (_) => setState(() => _emailError = null),
              ),

              const SizedBox(height: 24),

              // Password field
              VInputField(
                label: 'Password',
                hint: 'Your password',
                controller: _passwordController,
                errorText: _passwordError,
                obscureText: _obscurePassword,
                textInputAction: TextInputAction.done,
                onChanged: (_) => setState(() => _passwordError = null),
                onSubmitted: (_) => _submit(),
                suffixIcon: IconButton(
                  icon: Icon(
                    _obscurePassword ? Icons.visibility_off_rounded : Icons.visibility_rounded,
                    color: AppColors.textMuted,
                    size: 20,
                  ),
                  onPressed: () => setState(() => _obscurePassword = !_obscurePassword),
                ),
              ),

              // Forgot password
              Align(
                alignment: Alignment.centerRight,
                child: TextButton(
                  onPressed: () => context.push('/forgot-password'),
                  style: TextButton.styleFrom(
                    foregroundColor: AppColors.postbookPrimary,
                    padding: const EdgeInsets.symmetric(vertical: 12),
                  ),
                  child: Text(
                    'Forgot Password?',
                    style: AppTextStyles.bodySmall.copyWith(
                      color: AppColors.postbookPrimary,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
              ),

              const SizedBox(height: 12),

              // Log In button
              _GradientButton(
                label: 'Log In',
                loading: _loading,
                onTap: _loading ? null : _submit,
              ),

              const SizedBox(height: 40),

              // Divider
              Row(
                children: [
                  Expanded(child: Divider(color: AppColors.borderSubtle)),
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    child: Text(
                      'OR',
                      style: AppTextStyles.label.copyWith(color: AppColors.textDim),
                    ),
                  ),
                  Expanded(child: Divider(color: AppColors.borderSubtle)),
                ],
              ),

              const SizedBox(height: 32),

              // Register link
              Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Text(
                    "Don't have an account?",
                    style: AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
                  ),
                  TextButton(
                    onPressed: () => context.push('/register'),
                    child: Text(
                      'Register Now',
                      style: AppTextStyles.bodySmall.copyWith(
                        color: AppColors.postbookPrimary,
                        fontWeight: FontWeight.bold,
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

class _GradientButton extends StatelessWidget {
  final String label;
  final bool loading;
  final VoidCallback? onTap;

  const _GradientButton({required this.label, this.loading = false, this.onTap});

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 56,
      decoration: BoxDecoration(
        gradient: onTap != null ? AppColors.ctaGradient : null,
        color: onTap == null ? AppColors.textDim.withOpacity(0.3) : null,
        borderRadius: BorderRadius.circular(16),
        boxShadow: onTap != null ? [
          BoxShadow(
            color: AppColors.postbookPrimary.withOpacity(0.3),
            blurRadius: 12,
            offset: const Offset(0, 4),
          )
        ] : null,
      ),
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(16),
          child: Center(
            child: loading
                ? const SizedBox(
                    width: 24,
                    height: 24,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      valueColor: AlwaysStoppedAnimation<Color>(Colors.white),
                    ),
                  )
                : Text(
                    label,
                    style: AppTextStyles.h3.copyWith(
                      color: Colors.white,
                      fontWeight: FontWeight.bold,
                    ),
                  ),
          ),
        ),
      ),
    );
  }
}
