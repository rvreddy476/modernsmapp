import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/core/utils/validators.dart';
import 'package:atpost_app/core/widgets/app_toast.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/shared/widgets/v_input_field.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class RegisterScreen extends ConsumerStatefulWidget {
  const RegisterScreen({super.key});

  @override
  ConsumerState<RegisterScreen> createState() => _RegisterScreenState();
}

class _RegisterScreenState extends ConsumerState<RegisterScreen> {
  final _firstNameController = TextEditingController();
  final _lastNameController = TextEditingController();
  final _emailController = TextEditingController();
  final _passwordController = TextEditingController();
  final _confirmPasswordController = TextEditingController();

  // Field error states
  String? _firstNameError;
  String? _lastNameError;
  String? _emailError;
  String? _passwordError;
  String? _confirmPasswordError;
  String? _dobError;

  bool _loading = false;
  bool _obscurePassword = true;
  bool _obscureConfirm = true;

  DateTime? _dob;
  bool _acceptedTerms = false;

  static const _termsVersion = '2026-08-01';
  static const _minimumAge = 18;

  int _ageInYears(DateTime born, DateTime now) {
    var years = now.year - born.year;
    if (now.month < born.month ||
        (now.month == born.month && now.day < born.day)) {
      years--;
    }
    return years;
  }

  Future<void> _pickDateOfBirth() async {
    final now = DateTime.now();
    final initial = DateTime(now.year - _minimumAge, now.month, now.day);
    final picked = await showDatePicker(
      context: context,
      initialDate: _dob ?? initial,
      firstDate: DateTime(now.year - 120),
      lastDate: now,
      builder: (context, child) {
        return Theme(
          data: Theme.of(context).copyWith(
            colorScheme: const ColorScheme.dark(
              primary: AppColors.postbookPrimary,
              onPrimary: Colors.white,
              surface: AppColors.bgTertiary,
              onSurface: Colors.white,
            ),
          ),
          child: child!,
        );
      },
    );
    if (picked != null) {
      setState(() {
        _dob = picked;
        _dobError = null;
      });
    }
  }

  String _formatDob(DateTime d) =>
      '${d.year.toString().padLeft(4, '0')}-'
      '${d.month.toString().padLeft(2, '0')}-'
      '${d.day.toString().padLeft(2, '0')}';

  @override
  void dispose() {
    _firstNameController.dispose();
    _lastNameController.dispose();
    _emailController.dispose();
    _passwordController.dispose();
    _confirmPasswordController.dispose();
    super.dispose();
  }

  bool _validate() {
    setState(() {
      _firstNameError = Validators.required(_firstNameController.text, 'First Name');
      _lastNameError = Validators.required(_lastNameController.text, 'Last Name');

      _emailError = Validators.email(_emailController.text);
      _passwordError = Validators.password(_passwordController.text);
      _confirmPasswordError = Validators.confirmPassword(
        _confirmPasswordController.text,
        _passwordController.text,
      );

      if (_dob == null) {
        _dobError = 'Date of birth is required';
      } else if (_ageInYears(_dob!, DateTime.now()) < _minimumAge) {
        _dobError = 'You must be at least $_minimumAge years old';
      } else {
        _dobError = null;
      }
    });

    return _firstNameError == null &&
        _lastNameError == null &&
        _emailError == null &&
        _passwordError == null &&
        _confirmPasswordError == null &&
        _dobError == null &&
        _acceptedTerms;
  }

  Future<void> _submit() async {
    if (!_validate()) {
      if (!_acceptedTerms) {
        AppToast.error(context, 'Please accept the Terms and Privacy Policy');
      }
      return;
    }

    setState(() => _loading = true);

    try {
      final response = await ref.read(apiClientProvider).post(
        '${Environment.authPath}/register',
        data: {
          'email': _emailController.text.trim(),
          'password': _passwordController.text,
          'first_name': _firstNameController.text.trim(),
          'last_name': _lastNameController.text.trim(),
          'dob': _formatDob(_dob!),
          'accepted_terms': _acceptedTerms,
          'terms_version': _termsVersion,
        },
      );

      final data = response.data['data'] as Map<String, dynamic>?;
      if (data == null) throw Exception('Unexpected response format.');

      final tokens = data['tokens'] as Map<String, dynamic>? ?? data;
      final user = data['user'] as Map<String, dynamic>?;
      final userId = user?['id'] as String? ?? data['user_id'] as String? ?? '';
      final token = tokens['access_token'] as String? ?? '';
      final refreshToken = tokens['refresh_token'] as String?;

      if (!mounted) return;

      final requiresVerification = data['requires_verification'] == true || token.isEmpty;

      if (requiresVerification) {
        final email = _emailController.text.trim();
        AppToast.success(
          context,
          'Account created! Check your email for a 6-digit code.',
        );
        final vt = data['verification_token'] as String? ?? '';
        context.go(
          '/verify-otp?id=${Uri.encodeQueryComponent(email)}'
          '&mode=register'
          '&vt=${Uri.encodeQueryComponent(vt)}',
        );
        return;
      }

      ref.read(authServiceProvider).setSession(
        userId: userId,
        token: token,
        refreshToken: refreshToken,
      );

      AppToast.success(context, 'Welcome to VChat!');
      context.go('/');
    } on DioException catch (e) {
      if (!mounted) return;
      final serverCode = ErrorHandler.extractServerCode(e);

      if (serverCode == 'USER_EXISTS' || serverCode == 'EMAIL_EXISTS') {
        setState(() => _emailError = 'This email is already registered');
      } else {
        // Try to show specific validation errors from server if available
        final serverMessage = ErrorHandler.userMessageFor(e);
        if (serverMessage.contains('FirstName') || serverMessage.contains('first_name')) {
          setState(() => _firstNameError = 'First name is required');
        } else if (serverMessage.contains('LastName') || serverMessage.contains('last_name')) {
          setState(() => _lastNameError = 'Last name is required');
        } else {
          AppToast.error(context, serverMessage);
        }
      }
    } catch (_) {
      if (!mounted) return;
      AppToast.error(context, 'Could not create account. Please try again.');
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
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textSecondary,
            size: 20,
          ),
          onPressed: () => context.pop(),
        ),
        title: Text('Create Account', style: AppTextStyles.h2),
        centerTitle: true,
      ),
      body: SafeArea(
        child: SingleChildScrollView(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 32),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const SizedBox(height: 16),
              Text(
                'Join VChat and connect with the world.',
                style: AppTextStyles.body.copyWith(color: AppColors.textTertiary),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 32),

              // First & Last Name row
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Expanded(
                    child: VInputField(
                      label: 'First Name',
                      hint: 'John',
                      controller: _firstNameController,
                      isMandatory: true,
                      errorText: _firstNameError,
                      textInputAction: TextInputAction.next,
                      onChanged: (_) => setState(() => _firstNameError = null),
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: VInputField(
                      label: 'Last Name',
                      hint: 'Doe',
                      controller: _lastNameController,
                      isMandatory: true,
                      errorText: _lastNameError,
                      textInputAction: TextInputAction.next,
                      onChanged: (_) => setState(() => _lastNameError = null),
                    ),
                  ),
                ],
              ),

              const SizedBox(height: 20),

              // Email
              VInputField(
                label: 'Email',
                hint: 'you@example.com',
                controller: _emailController,
                isMandatory: true,
                errorText: _emailError,
                keyboardType: TextInputType.emailAddress,
                textInputAction: TextInputAction.next,
                onChanged: (_) => setState(() => _emailError = null),
              ),

              const SizedBox(height: 20),

              // Password
              VInputField(
                label: 'Password',
                hint: 'At least 8 characters',
                controller: _passwordController,
                isMandatory: true,
                errorText: _passwordError,
                obscureText: _obscurePassword,
                textInputAction: TextInputAction.next,
                onChanged: (_) => setState(() => _passwordError = null),
                suffixIcon: IconButton(
                  icon: Icon(
                    _obscurePassword ? Icons.visibility_off_rounded : Icons.visibility_rounded,
                    color: AppColors.textMuted,
                    size: 20,
                  ),
                  onPressed: () => setState(() => _obscurePassword = !_obscurePassword),
                ),
              ),

              const SizedBox(height: 20),

              // Confirm Password
              VInputField(
                label: 'Confirm Password',
                hint: 'Repeat your password',
                controller: _confirmPasswordController,
                isMandatory: true,
                errorText: _confirmPasswordError,
                obscureText: _obscureConfirm,
                textInputAction: TextInputAction.done,
                onChanged: (_) => setState(() => _confirmPasswordError = null),
                onSubmitted: (_) => _submit(),
                suffixIcon: IconButton(
                  icon: Icon(
                    _obscureConfirm ? Icons.visibility_off_rounded : Icons.visibility_rounded,
                    color: AppColors.textMuted,
                    size: 20,
                  ),
                  onPressed: () => setState(() => _obscureConfirm = !_obscureConfirm),
                ),
              ),

              const SizedBox(height: 20),

              // Date of Birth
              _buildDobPicker(),

              const SizedBox(height: 24),

              // Terms and Conditions
              _buildTermsCheckbox(),

              const SizedBox(height: 32),

              // Submit Button
              _GradientButton(
                label: 'Create Account',
                loading: _loading,
                onTap: _loading ? null : _submit,
              ),

              const SizedBox(height: 24),

              // Login Redirect
              _buildLoginLink(),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildDobPicker() {
    final hasError = _dobError != null;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Text(
              'Date of birth',
              style: AppTextStyles.label.copyWith(
                color: hasError ? AppColors.statusError : AppColors.textSecondary,
                fontWeight: FontWeight.w600,
              ),
            ),
            Padding(
              padding: const EdgeInsets.only(left: 4),
              child: Text(
                '*',
                style: AppTextStyles.label.copyWith(color: AppColors.statusError),
              ),
            ),
          ],
        ),
        const SizedBox(height: 8),
        InkWell(
          onTap: _loading ? null : _pickDateOfBirth,
          borderRadius: BorderRadius.circular(16),
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 18),
            decoration: BoxDecoration(
              color: hasError ? AppColors.statusError.withOpacity(0.05) : AppColors.bgCard,
              borderRadius: BorderRadius.circular(16),
              border: Border.all(
                color: hasError ? AppColors.statusError : AppColors.borderSubtle,
              ),
              boxShadow: hasError ? [
                BoxShadow(
                  color: AppColors.statusError.withOpacity(0.1),
                  blurRadius: 10,
                  spreadRadius: 2,
                )
              ] : null,
            ),
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    _dob == null ? 'Select your date of birth' : _formatDob(_dob!),
                    style: AppTextStyles.body.copyWith(
                      color: _dob == null ? AppColors.textMuted : AppColors.textPrimary,
                    ),
                  ),
                ),
                const Icon(
                  Icons.calendar_today_rounded,
                  size: 18,
                  color: AppColors.textMuted,
                ),
              ],
            ),
          ),
        ),
        if (hasError)
          Padding(
            padding: const EdgeInsets.only(top: 8, left: 4),
            child: Text(
              _dobError!,
              style: AppTextStyles.bodySmall.copyWith(color: AppColors.statusError),
            ),
          ),
        const SizedBox(height: 6),
        Text(
          'You must be at least $_minimumAge to use VChat.',
          style: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
        ),
      ],
    );
  }

  Widget _buildTermsCheckbox() {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          height: 24,
          width: 24,
          child: Checkbox(
            value: _acceptedTerms,
            activeColor: AppColors.postbookPrimary,
            shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(4)),
            onChanged: _loading ? null : (v) => setState(() => _acceptedTerms = v ?? false),
          ),
        ),
        const SizedBox(width: 12),
        Expanded(
          child: Text(
            'I agree to the Terms of Service and the Privacy Policy.',
            style: AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
          ),
        ),
      ],
    );
  }

  Widget _buildLoginLink() {
    return Row(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        Text('Already have an account?', style: AppTextStyles.bodySmall),
        TextButton(
          onPressed: () => context.pop(),
          style: TextButton.styleFrom(foregroundColor: AppColors.postbookPrimary),
          child: Text(
            'Log In',
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.postbookPrimary,
              fontWeight: FontWeight.w700,
            ),
          ),
        ),
      ],
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
                    style: AppTextStyles.h3.copyWith(color: Colors.white, fontWeight: FontWeight.bold),
                  ),
          ),
        ),
      ),
    );
  }
}
