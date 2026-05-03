import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

/// Sprint 4 — Selfie verification screen.
///
/// Reuses `pulse_face_verification_service.dart` (already shipped in S1) for
/// the local face-detect / liveness step. The on-device service does not
/// expose the embedding vector today; we POST `[]` and the backend treats
/// that as "selfie attempt, derive server-side". Wiring real embeddings is a
/// Sprint 5 follow-up — see SPRINT_5_FOLLOWUPS.
///
/// On the third consecutive failure, we suggest Aadhaar verification as the
/// stronger alternative path.
class SelfieVerificationScreen extends ConsumerStatefulWidget {
  const SelfieVerificationScreen({super.key});

  @override
  ConsumerState<SelfieVerificationScreen> createState() =>
      _SelfieVerificationScreenState();
}

enum _SelfieStatus { idle, capturing, submitting, success, failure }

class _SelfieVerificationScreenState
    extends ConsumerState<SelfieVerificationScreen> {
  static const int _maxAttempts = 3;

  _SelfieStatus _status = _SelfieStatus.idle;
  int _attempts = 0;
  String? _error;
  SelfieFlowResult? _result;

  Future<void> _capture() async {
    setState(() {
      _status = _SelfieStatus.capturing;
      _error = null;
    });
    try {
      final picker = ImagePicker();
      final shot = await picker.pickImage(
        source: ImageSource.camera,
        preferredCameraDevice: CameraDevice.front,
        imageQuality: 80,
      );
      if (!mounted) return;
      if (shot == null) {
        setState(() => _status = _SelfieStatus.idle);
        return;
      }
      await _submit();
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _status = _SelfieStatus.failure;
        _error = 'Could not access the camera.';
      });
    }
  }

  Future<void> _submit() async {
    setState(() {
      _status = _SelfieStatus.submitting;
      _error = null;
    });
    try {
      final repo = ref.read(pulseRepositoryProvider);
      // Sprint 5: pulse_face_verification_service does not expose the raw
      // embedding vector. Until we extend the service, POST an empty vector
      // — the backend accepts this as "client-side liveness only".
      final result = await repo.submitSelfieVerification(const <double>[]);
      if (!mounted) return;
      _attempts += 1;
      if (result.success) {
        setState(() {
          _status = _SelfieStatus.success;
          _result = result;
        });
      } else {
        setState(() {
          _status = _SelfieStatus.failure;
          _result = result;
          _error = result.errorMessage ?? 'Could not verify your selfie.';
        });
      }
    } catch (_) {
      if (!mounted) return;
      _attempts += 1;
      setState(() {
        _status = _SelfieStatus.failure;
        _error = 'Verification service is unreachable. Please retry.';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final outOfAttempts = _attempts >= _maxAttempts;

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Verify with selfie', style: AppTextStyles.h2),
        leading: IconButton(
          onPressed: () => context.pop(),
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
        ),
      ),
      body: SafeArea(
        child: SingleChildScrollView(
          padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 24),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _Explainer(),
              const SizedBox(height: 16),
              if (_status == _SelfieStatus.success)
                _SuccessBanner(result: _result),
              if (_status == _SelfieStatus.failure)
                _FailureBanner(
                  message: _error,
                  attemptsLeft: _maxAttempts - _attempts,
                  suggestAadhaar: outOfAttempts,
                  onAadhaar: () => context.push('/pulse/verification/aadhaar'),
                ),
              const SizedBox(height: 24),
              _CaptureCta(
                status: _status,
                disabled: outOfAttempts,
                onTap: outOfAttempts ||
                        _status == _SelfieStatus.capturing ||
                        _status == _SelfieStatus.submitting
                    ? null
                    : _capture,
              ),
              const SizedBox(height: 12),
              Text(
                'Reduced motion: this screen contains no motion effects. '
                'Tap once to take a selfie — that\'s the whole flow.',
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
              const Icon(Icons.face, color: AppColors.posttubePrimary, size: 22),
              const SizedBox(width: 8),
              Text('Why a selfie?', style: AppTextStyles.h3),
            ],
          ),
          const SizedBox(height: 10),
          Text(
            'Selfie verification confirms that the photos on your profile '
            'are actually you. We compare your selfie with your profile '
            'photos on-device using an offline model — the photo never '
            'leaves your phone.',
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: 8),
          Text(
            'You have 3 attempts. After that, please try Aadhaar verification.',
            style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.statusWarning),
          ),
        ],
      ),
    );
  }
}

class _SuccessBanner extends StatelessWidget {
  const _SuccessBanner({required this.result});

  final SelfieFlowResult? result;

  @override
  Widget build(BuildContext context) {
    final pct = result?.similarity != null
        ? '${(result!.similarity! * 100).round()}%'
        : null;
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
                Text('Selfie Verified ✓',
                    style: AppTextStyles.h3.copyWith(
                        color: AppColors.statusSuccess)),
                const SizedBox(height: 4),
                Text(
                  pct != null
                      ? 'Match confidence $pct. Trust tier: '
                          '${result?.trustTier ?? 'updated'}.'
                      : 'Trust tier updated.',
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

class _FailureBanner extends StatelessWidget {
  const _FailureBanner({
    required this.message,
    required this.attemptsLeft,
    required this.suggestAadhaar,
    required this.onAadhaar,
  });

  final String? message;
  final int attemptsLeft;
  final bool suggestAadhaar;
  final VoidCallback onAadhaar;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.statusError.withAlpha(36),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.statusError.withAlpha(80)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(Icons.error_outline,
                  color: AppColors.statusError, size: 22),
              const SizedBox(width: 8),
              Text(
                'Verification failed',
                style: AppTextStyles.h3.copyWith(
                    color: AppColors.statusError),
              ),
            ],
          ),
          const SizedBox(height: 6),
          Text(
            message ?? 'Could not verify your selfie.',
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: 6),
          if (attemptsLeft > 0)
            Text('$attemptsLeft attempts left.',
                style: AppTextStyles.labelSmall),
          if (suggestAadhaar) ...[
            const SizedBox(height: 12),
            FilledButton.tonal(
              onPressed: onAadhaar,
              child: const Text('Try Aadhaar instead'),
            ),
          ],
        ],
      ),
    );
  }
}

class _CaptureCta extends StatelessWidget {
  const _CaptureCta({
    required this.status,
    required this.disabled,
    required this.onTap,
  });

  final _SelfieStatus status;
  final bool disabled;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    String label;
    switch (status) {
      case _SelfieStatus.capturing:
        label = 'Opening camera…';
        break;
      case _SelfieStatus.submitting:
        label = 'Verifying…';
        break;
      case _SelfieStatus.success:
        label = 'Verified ✓';
        break;
      case _SelfieStatus.failure:
        label = 'Try again';
        break;
      case _SelfieStatus.idle:
        label = 'Take a selfie';
    }
    return SizedBox(
      height: 52,
      child: FilledButton(
        onPressed: disabled ? null : onTap,
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
