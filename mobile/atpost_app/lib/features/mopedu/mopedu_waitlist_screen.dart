import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/mopedu_repository.dart';
import 'package:atpost_app/services/mopedu_crash_breadcrumbs.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Sprint 5 — city-gated waitlist screen for Mopedu.
///
/// Shown by `MopeduGate` when the master `mopedu_enabled_master` flag is
/// ON but the user's city is not in `kMopeduAllowedCitiesV1` (Bengaluru
/// / Bangalore at v1).
///
/// Captures email and posts via `MopeduRepository.joinWaitlist`. Backend
/// implements the endpoint in Sprint 6 — until then the call is a no-op
/// against a stub endpoint, and 404 / 501 / "not found" responses are
/// treated as success so the UI flows the user through to confirmation.
class MopeduWaitlistScreen extends ConsumerStatefulWidget {
  const MopeduWaitlistScreen({super.key, this.userCity});

  final String? userCity;

  @override
  ConsumerState<MopeduWaitlistScreen> createState() =>
      _MopeduWaitlistScreenState();
}

class _MopeduWaitlistScreenState extends ConsumerState<MopeduWaitlistScreen> {
  final _emailController = TextEditingController();
  bool _submitting = false;
  bool _submitted = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    MopeduBreadcrumbs.add(
      'mopedu.city_gate.shown',
      data: {'city': widget.userCity ?? ''},
    );
  }

  @override
  void dispose() {
    _emailController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final email = _emailController.text.trim();
    if (email.isEmpty || !email.contains('@')) {
      setState(() => _error = 'Please enter a valid email.');
      return;
    }
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      await ref.read(mopeduRepositoryProvider).joinWaitlist(
            city: widget.userCity ?? '',
            email: email,
          );
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _submitted = true;
      });
      MopeduBreadcrumbs.waitlistJoined(city: widget.userCity);
    } catch (e, st) {
      MopeduBreadcrumbs.error(
        'mopedu waitlist submit failed',
        error: e,
        stackTrace: st,
      );
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = 'Could not save you to the waitlist. Try again later.';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final city = widget.userCity ?? 'your city';
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Mopedu', style: AppTextStyles.h2),
      ),
      body: SingleChildScrollView(
        padding: AppSpacing.pagePadding,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const SizedBox(height: 32),
            const Icon(
              Icons.location_on_outlined,
              size: 64,
              color: AppColors.posttubePrimary,
            ),
            const SizedBox(height: 24),
            Text(
              'Coming to $city soon.',
              style: AppTextStyles.h2,
              textAlign: TextAlign.center,
            ),
            const SizedBox(height: 12),
            Text(
              "Mopedu is launching one city at a time. We'll prioritise "
              'opening up next where the most people are waiting.',
              style: AppTextStyles.body.copyWith(
                color: AppColors.textSecondary,
              ),
              textAlign: TextAlign.center,
            ),
            const SizedBox(height: 32),
            if (_submitted)
              Container(
                padding: const EdgeInsets.all(16),
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius: BorderRadius.circular(
                    AppSpacing.radiusLarge,
                  ),
                  border: Border.all(color: AppColors.borderSubtle),
                ),
                child: Text(
                  "You're on the waitlist. We'll email you as soon as "
                  "Mopedu opens for $city.",
                  style: AppTextStyles.body.copyWith(
                    color: AppColors.textPrimary,
                  ),
                  textAlign: TextAlign.center,
                ),
              )
            else ...[
              Text(
                'Add yourself to the waitlist.',
                style: AppTextStyles.body,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _emailController,
                keyboardType: TextInputType.emailAddress,
                style: AppTextStyles.body.copyWith(
                  color: AppColors.textPrimary,
                ),
                decoration: InputDecoration(
                  hintText: 'you@example.com',
                  hintStyle: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textMuted,
                  ),
                  filled: true,
                  fillColor: AppColors.bgCard,
                  errorText: _error,
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(
                      AppSpacing.radiusLarge,
                    ),
                    borderSide:
                        const BorderSide(color: AppColors.borderSubtle),
                  ),
                ),
              ),
              const SizedBox(height: 16),
              FilledButton(
                onPressed: _submitting ? null : _submit,
                style: FilledButton.styleFrom(
                  backgroundColor: AppColors.posttubePrimary,
                  padding: const EdgeInsets.symmetric(vertical: 14),
                ),
                child: _submitting
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.white,
                        ),
                      )
                    : const Text('Join the waitlist'),
              ),
            ],
          ],
        ),
      ),
    );
  }
}
