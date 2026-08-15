import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/core/widgets/app_toast.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/services/auth_service.dart';
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
  final _displayNameController = TextEditingController();
  final _emailController = TextEditingController();
  final _passwordController = TextEditingController();
  final _confirmPasswordController = TextEditingController();

  bool _loading = false;
  bool _obscurePassword = true;
  bool _obscureConfirm = true;

  // Module 3 M3-P0-3 / SR-6 — date of birth and consent are now REQUIRED.
  //
  // The backend age gate was 13 and was skipped entirely when no date of birth
  // was supplied — and this screen supplied none, so every account it created
  // bypassed the check. There was no consent capture at all.
  //
  // The gate is now 18 and mandatory. India's DPDP Act requires verifiable
  // parental consent to process the data of anyone under 18, and this platform
  // has no such flow, so it cannot lawfully onboard a minor.
  DateTime? _dob;
  bool _acceptedTerms = false;

  /// Must match service.CurrentTermsVersion in auth-service. The server
  /// rejects a mismatch, so a stale client fails loudly rather than recording
  /// consent to a text the user never saw.
  static const _termsVersion = '2026-08-01';
  static const _minimumAge = 18;

  /// Completed years, computed the same way the server does: an explicit
  /// month/day comparison, not a division by 365.25, because on a legal
  /// boundary a one-day error admits someone the platform cannot onboard.
  int _ageInYears(DateTime born, DateTime now) {
    var years = now.year - born.year;
    if (now.month < born.month || (now.month == born.month && now.day < born.day)) {
      years--;
    }
    return years;
  }

  Future<void> _pickDateOfBirth() async {
    final now = DateTime.now();
    // Open the picker at the earliest allowed date rather than today, so the
    // user is not scrolling back eighteen years from a default that can never
    // be valid.
    final initial = DateTime(now.year - _minimumAge, now.month, now.day);
    final picked = await showDatePicker(
      context: context,
      initialDate: _dob ?? initial,
      firstDate: DateTime(now.year - 120),
      lastDate: now,
      helpText: 'Select your date of birth',
    );
    if (picked != null) {
      setState(() => _dob = picked);
    }
  }

  String _formatDob(DateTime d) =>
      '${d.year.toString().padLeft(4, '0')}-'
      '${d.month.toString().padLeft(2, '0')}-'
      '${d.day.toString().padLeft(2, '0')}';

  @override
  void dispose() {
    _displayNameController.dispose();
    _emailController.dispose();
    _passwordController.dispose();
    _confirmPasswordController.dispose();
    super.dispose();
  }

  String? _validate() {
    final displayName = _displayNameController.text.trim();
    final email = _emailController.text.trim();
    final password = _passwordController.text;
    final confirm = _confirmPasswordController.text;

    if (displayName.isEmpty) return 'Display name is required.';
    if (email.isEmpty) return 'Email is required.';
    if (password.isEmpty) return 'Password is required.';
    if (password.length < 8) return 'Password must be at least 8 characters.';
    if (password != confirm) return 'Passwords do not match.';

    // SR-6. These are checked here only so the user gets an immediate answer;
    // the server enforces the same rules and is the authority. A client-side
    // check alone would be trivially bypassed.
    if (_dob == null) return 'Date of birth is required.';
    if (_ageInYears(_dob!, DateTime.now()) < _minimumAge) {
      return 'You must be at least $_minimumAge years old to create an account.';
    }
    if (!_acceptedTerms) {
      return 'You must accept the Terms of Service and Privacy Policy.';
    }
    return null;
  }

  Future<void> _submit() async {
    final error = _validate();
    if (error != null) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(error)));
      return;
    }

    setState(() => _loading = true);

    try {
      // Split display name into first/last for backend
      final nameParts = _displayNameController.text.trim().split(RegExp(r'\s+'));
      final firstName = nameParts.first;
      final lastName = nameParts.length > 1 ? nameParts.sublist(1).join(' ') : '';

      final response = await ref
          .read(apiClientProvider)
          .post(
            '${Environment.authPath}/register',
            data: {
              'email': _emailController.text.trim(),
              'password': _passwordController.text,
              'first_name': firstName,
              'last_name': lastName,
              // SR-6: the server requires all three. `dob` was previously
              // omitted, which skipped the age gate entirely.
              'dob': _formatDob(_dob!),
              'accepted_terms': _acceptedTerms,
              'terms_version': _termsVersion,
            },
          );

      final data = response.data['data'] as Map<String, dynamic>?;
      if (data == null) {
        throw Exception('Unexpected response format.');
      }

      final tokens = data['tokens'] as Map<String, dynamic>? ?? data;
      final user = data['user'] as Map<String, dynamic>?;
      final userId = user?['id'] as String? ?? data['user_id'] as String? ?? '';
      final token = tokens['access_token'] as String? ?? '';
      final refreshToken = tokens['refresh_token'] as String?;

      if (!mounted) return;

      // A new account is created PENDING. The server returns empty tokens and
      // requires_verification=true until the emailed code is entered, so
      // treating registration as a completed sign-in set an empty session and
      // dropped the user on a home screen that could not load anything —
      // with nothing on screen explaining that a code was waiting for them.
      final requiresVerification =
          data['requires_verification'] == true || token.isEmpty;

      if (requiresVerification) {
        final email = _emailController.text.trim();
        AppToast.success(
          context,
          'Account created. We sent a 6-digit code to $email — enter it to finish.',
        );
        final vt = data['verification_token'] as String? ?? '';
        context.go(
          '/verify-otp?id=${Uri.encodeQueryComponent(email)}'
          '&mode=register'
          '&vt=${Uri.encodeQueryComponent(vt)}',
        );
        return;
      }

      ref
          .read(authServiceProvider)
          .setSession(userId: userId, token: token, refreshToken: refreshToken);

      AppToast.success(context, 'Welcome to atPost!');
      context.go('/');
    } on DioException catch (e) {
      if (!mounted) return;
      // One line, because the mapping now lives in UserMessages: server code
      // first ("USER_EXISTS" -> "This email is already registered"), then
      // status, then the server's own sentence. The previous unwrapping here
      // surfaced whatever the backend happened to say, including bare codes.
      AppToast.error(context, ErrorHandler.userMessageFor(e));
    } catch (_) {
      if (!mounted) return;
      AppToast.error(
        context,
        'We could not create your account. Please try again.',
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

              // Subheading
              Text(
                'Join VChat and connect with the world.',
                style: AppTextStyles.body.copyWith(
                  color: AppColors.textTertiary,
                ),
                textAlign: TextAlign.center,
              ),

              const SizedBox(height: 32),

              // Display Name
              _buildLabel('Display Name'),
              const SizedBox(height: 6),
              _buildTextField(
                controller: _displayNameController,
                hint: 'Your full name',
                textInputAction: TextInputAction.next,
              ),

              const SizedBox(height: 16),

              // Email
              _buildLabel('Email'),
              const SizedBox(height: 6),
              _buildTextField(
                controller: _emailController,
                hint: 'you@example.com',
                keyboardType: TextInputType.emailAddress,
                textInputAction: TextInputAction.next,
              ),

              const SizedBox(height: 16),

              // Password
              _buildLabel('Password'),
              const SizedBox(height: 6),
              _buildTextField(
                controller: _passwordController,
                hint: 'At least 8 characters',
                obscureText: _obscurePassword,
                textInputAction: TextInputAction.next,
                suffixIcon: _visibilityToggle(
                  obscured: _obscurePassword,
                  onTap: () =>
                      setState(() => _obscurePassword = !_obscurePassword),
                ),
              ),

              const SizedBox(height: 16),

              // Confirm Password
              _buildLabel('Confirm Password'),
              const SizedBox(height: 6),
              _buildTextField(
                controller: _confirmPasswordController,
                hint: 'Repeat your password',
                obscureText: _obscureConfirm,
                textInputAction: TextInputAction.done,
                onSubmitted: (_) => _submit(),
                suffixIcon: _visibilityToggle(
                  obscured: _obscureConfirm,
                  onTap: () =>
                      setState(() => _obscureConfirm = !_obscureConfirm),
                ),
              ),

              const SizedBox(height: 16),

              // SR-6: date of birth. Required, and the 18+ rule is stated
              // before the user picks rather than after they are rejected.
              _buildLabel('Date of birth'),
              const SizedBox(height: 6),
              InkWell(
                onTap: _loading ? null : _pickDateOfBirth,
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                child: InputDecorator(
                  decoration: InputDecoration(
                    filled: true,
                    fillColor: AppColors.bgSecondary,
                    contentPadding: const EdgeInsets.symmetric(
                      horizontal: 16,
                      vertical: 16,
                    ),
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                      borderSide: BorderSide(color: AppColors.borderSubtle),
                    ),
                    enabledBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                      borderSide: BorderSide(color: AppColors.borderSubtle),
                    ),
                    suffixIcon: const Icon(
                      Icons.calendar_today_outlined,
                      size: 18,
                      color: AppColors.textMuted,
                    ),
                  ),
                  child: Text(
                    _dob == null ? 'Select your date of birth' : _formatDob(_dob!),
                    style: AppTextStyles.body.copyWith(
                      color: _dob == null
                          ? AppColors.textMuted
                          : AppColors.textPrimary,
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 6),
              Text(
                'You must be at least $_minimumAge to use atPost.',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textSecondary,
                ),
              ),

              const SizedBox(height: 16),

              // SR-6: explicit consent. Unchecked by default — a pre-ticked
              // box is not consent, and the server refuses a registration
              // whose accepted_terms is false.
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Checkbox(
                    value: _acceptedTerms,
                    onChanged: _loading
                        ? null
                        : (v) => setState(() => _acceptedTerms = v ?? false),
                  ),
                  Expanded(
                    child: Padding(
                      padding: const EdgeInsets.only(top: 12),
                      child: Text(
                        'I agree to the Terms of Service and the Privacy Policy.',
                        style: AppTextStyles.bodySmall.copyWith(
                          color: AppColors.textSecondary,
                        ),
                      ),
                    ),
                  ),
                ],
              ),

              const SizedBox(height: 32),

              // Register button
              _GradientButton(
                label: 'Create Account',
                loading: _loading,
                onTap: _loading ? null : _submit,
              ),

              const SizedBox(height: 24),

              // Already have account
              Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Text(
                    'Already have an account?',
                    style: AppTextStyles.bodySmall,
                  ),
                  TextButton(
                    onPressed: () => context.pop(),
                    style: TextButton.styleFrom(
                      foregroundColor: AppColors.postbookPrimary,
                      padding: const EdgeInsets.symmetric(
                        horizontal: 6,
                        vertical: 4,
                      ),
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    ),
                    child: Text(
                      'Log In',
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

  Widget _buildLabel(String text) {
    return Text(
      text,
      style: AppTextStyles.label.copyWith(color: AppColors.textSecondary),
    );
  }

  Widget _visibilityToggle({
    required bool obscured,
    required VoidCallback onTap,
  }) {
    return IconButton(
      icon: Icon(
        obscured ? Icons.visibility_off_outlined : Icons.visibility_outlined,
        color: AppColors.textMuted,
        size: 20,
      ),
      onPressed: onTap,
    );
  }

  Widget _buildTextField({
    required TextEditingController controller,
    required String hint,
    bool obscureText = false,
    TextInputType keyboardType = TextInputType.text,
    TextInputAction textInputAction = TextInputAction.next,
    Widget? suffixIcon,
    void Function(String)? onSubmitted,
  }) {
    return TextField(
      controller: controller,
      obscureText: obscureText,
      keyboardType: keyboardType,
      textInputAction: textInputAction,
      onSubmitted: onSubmitted,
      style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
      decoration: InputDecoration(
        hintText: hint,
        hintStyle: AppTextStyles.body.copyWith(color: AppColors.textDim),
        filled: true,
        fillColor: AppColors.bgCard,
        suffixIcon: suffixIcon,
        contentPadding: const EdgeInsets.symmetric(
          horizontal: 16,
          vertical: 14,
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
          borderSide: const BorderSide(
            color: AppColors.postbookPrimary,
            width: 1.5,
          ),
        ),
      ),
    );
  }
}

/// Reusable full-width gradient button.
class _GradientButton extends StatelessWidget {
  final String label;
  final bool loading;
  final VoidCallback? onTap;

  const _GradientButton({
    required this.label,
    this.loading = false,
    this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 52,
      decoration: BoxDecoration(
        gradient: onTap != null ? AppColors.postbookGradient : null,
        color: onTap == null ? AppColors.textDim : null,
        borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
      ),
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
          child: Center(
            child: loading
                ? const SizedBox(
                    width: 22,
                    height: 22,
                    child: CircularProgressIndicator(
                      strokeWidth: 2.5,
                      valueColor: AlwaysStoppedAnimation<Color>(Colors.white),
                    ),
                  )
                : Text(
                    label,
                    style: AppTextStyles.h3.copyWith(
                      color: Colors.white,
                      letterSpacing: 0.4,
                    ),
                  ),
          ),
        ),
      ),
    );
  }
}
