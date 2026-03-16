import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

const _kBrandRed = Color(0xFFD8103F);

class VerificationScreen extends ConsumerStatefulWidget {
  const VerificationScreen({super.key});

  @override
  ConsumerState<VerificationScreen> createState() => _VerificationScreenState();
}

class _VerificationScreenState extends ConsumerState<VerificationScreen> {
  String? _status;
  String _selectedType = 'individual';
  bool _isLoading = true;
  bool _isSubmitting = false;

  @override
  void initState() {
    super.initState();
    _loadStatus();
  }

  Future<void> _loadStatus() async {
    setState(() => _isLoading = true);
    try {
      final res =
          await ref.read(apiClientProvider).get('/v1/trust/verification');
      final data = res.data['data'] ?? res.data;
      if (mounted) {
        setState(() => _status = data['status'] as String?);
      }
    } on DioException catch (e) {
      if (e.response?.statusCode == 404) {
        if (mounted) setState(() => _status = null);
      }
    } catch (_) {
      // Network error — leave _status = null (show form)
    } finally {
      if (mounted) setState(() => _isLoading = false);
    }
  }

  Future<void> _submitApplication() async {
    setState(() => _isSubmitting = true);
    try {
      await ref.read(apiClientProvider).post(
        '/v1/trust/verification',
        data: {'type': _selectedType, 'docs': <String, dynamic>{}},
      );
      await _loadStatus();
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Failed to submit application. Please try again.'),
          ),
        );
      }
    } finally {
      if (mounted) setState(() => _isSubmitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new,
            color: AppColors.textPrimary,
          ),
          onPressed: () => context.pop(),
        ),
        title: Text('Get Verified', style: AppTextStyles.h2),
      ),
      body: _isLoading
          ? const Center(
              child: CircularProgressIndicator(color: _kBrandRed),
            )
          : SingleChildScrollView(
              padding: AppSpacing.pagePadding.copyWith(top: 24, bottom: 40),
              child: _buildBody(),
            ),
    );
  }

  Widget _buildBody() {
    if (_status == null) return _buildForm();
    if (_status == 'pending') return _buildPending();
    if (_status == 'approved') return _buildApproved();
    if (_status == 'rejected') return _buildRejected();
    return _buildForm();
  }

  // ---------------------------------------------------------------------------
  // Application form
  // ---------------------------------------------------------------------------
  Widget _buildForm() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // Header
        Row(
          children: [
            Container(
              width: 52,
              height: 52,
              decoration: BoxDecoration(
                color: const Color(0x1AD8103F),
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
              ),
              child: const Icon(Icons.verified, color: _kBrandRed, size: 28),
            ),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text('Apply for Verification', style: AppTextStyles.h2),
                  const SizedBox(height: 4),
                  Text(
                    'Apply for a verified badge to show your authenticity',
                    style: AppTextStyles.bodySmall
                        .copyWith(color: AppColors.textSecondary),
                  ),
                ],
              ),
            ),
          ],
        ),
        const SizedBox(height: 28),
        Text(
          'ACCOUNT TYPE',
          style:
              AppTextStyles.labelSmall.copyWith(color: AppColors.textMuted),
        ),
        const SizedBox(height: 12),
        ..._verificationTypes.map(
          (t) => _TypeRadioTile(
            type: t,
            selected: _selectedType == t.value,
            onTap: () => setState(() => _selectedType = t.value),
          ),
        ),
        const SizedBox(height: 28),
        SizedBox(
          width: double.infinity,
          child: ElevatedButton(
            onPressed: _isSubmitting ? null : _submitApplication,
            style: ElevatedButton.styleFrom(
              backgroundColor: _kBrandRed,
              foregroundColor: Colors.white,
              padding: const EdgeInsets.symmetric(vertical: 16),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
              ),
            ),
            child: _isSubmitting
                ? const SizedBox(
                    height: 20,
                    width: 20,
                    child: CircularProgressIndicator(
                        strokeWidth: 2, color: Colors.white),
                  )
                : Text('Submit Application', style: AppTextStyles.label),
          ),
        ),
      ],
    );
  }

  // ---------------------------------------------------------------------------
  // Status cards
  // ---------------------------------------------------------------------------
  Widget _buildPending() {
    return _StatusCard(
      color: const Color(0xFFF59E0B),
      bgColor: const Color(0x1AF59E0B),
      icon: Icons.hourglass_top_rounded,
      title: 'Under Review',
      message:
          'Your request is under review. We\'ll notify you within 5-7 business days.',
    );
  }

  Widget _buildApproved() {
    return _StatusCard(
      color: const Color(0xFF22C55E),
      bgColor: const Color(0x1A22C55E),
      icon: Icons.check_circle_outline_rounded,
      title: 'Verified',
      message: 'Congratulations! Your account is verified.',
    );
  }

  Widget _buildRejected() {
    return Column(
      children: [
        _StatusCard(
          color: _kBrandRed,
          bgColor: const Color(0x1AD8103F),
          icon: Icons.cancel_outlined,
          title: 'Not Approved',
          message: 'Your request was not approved.',
        ),
        const SizedBox(height: 20),
        SizedBox(
          width: double.infinity,
          child: OutlinedButton(
            onPressed: () => setState(() => _status = null),
            style: OutlinedButton.styleFrom(
              foregroundColor: _kBrandRed,
              side: const BorderSide(color: _kBrandRed),
              padding: const EdgeInsets.symmetric(vertical: 14),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
              ),
            ),
            child: Text('Apply Again', style: AppTextStyles.label),
          ),
        ),
      ],
    );
  }

  static const _verificationTypes = [
    _VerificationType(
      value: 'individual',
      label: 'Individual',
      subtitle: 'Personal accounts — journalists, athletes, public figures',
      icon: Icons.person_outline,
    ),
    _VerificationType(
      value: 'business',
      label: 'Business',
      subtitle: 'Companies, brands, and organisations',
      icon: Icons.business_outlined,
    ),
    _VerificationType(
      value: 'creator',
      label: 'Creator',
      subtitle: 'Content creators, influencers, and artists',
      icon: Icons.star_outline_rounded,
    ),
    _VerificationType(
      value: 'public_figure',
      label: 'Public Figure',
      subtitle: 'Politicians, executives, and prominent personalities',
      icon: Icons.emoji_events_outlined,
    ),
  ];
}

// ---------------------------------------------------------------------------
// Supporting data + widgets
// ---------------------------------------------------------------------------

class _VerificationType {
  final String value;
  final String label;
  final String subtitle;
  final IconData icon;

  const _VerificationType({
    required this.value,
    required this.label,
    required this.subtitle,
    required this.icon,
  });
}

class _TypeRadioTile extends StatelessWidget {
  const _TypeRadioTile({
    required this.type,
    required this.selected,
    required this.onTap,
  });

  final _VerificationType type;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 150),
        margin: const EdgeInsets.only(bottom: 10),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        decoration: BoxDecoration(
          color: selected
              ? const Color(0x1AD8103F)
              : AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(
            color: selected ? _kBrandRed : AppColors.borderSubtle,
            width: selected ? 1.5 : 1,
          ),
        ),
        child: Row(
          children: [
            Icon(
              type.icon,
              color:
                  selected ? _kBrandRed : AppColors.textSecondary,
              size: 22,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(type.label, style: AppTextStyles.label),
                  const SizedBox(height: 2),
                  Text(
                    type.subtitle,
                    style: AppTextStyles.labelSmall
                        .copyWith(color: AppColors.textSecondary),
                  ),
                ],
              ),
            ),
            Icon(
              selected
                  ? Icons.radio_button_checked
                  : Icons.radio_button_unchecked,
              color: selected ? _kBrandRed : AppColors.textMuted,
              size: 22,
            ),
          ],
        ),
      ),
    );
  }
}

class _StatusCard extends StatelessWidget {
  const _StatusCard({
    required this.color,
    required this.bgColor,
    required this.icon,
    required this.title,
    required this.message,
  });

  final Color color;
  final Color bgColor;
  final IconData icon;
  final String title;
  final String message;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: bgColor,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: color.withValues(alpha: 0.4)),
      ),
      child: Column(
        children: [
          Icon(icon, color: color, size: 48),
          const SizedBox(height: 14),
          Text(title,
              style: AppTextStyles.h2.copyWith(color: color)),
          const SizedBox(height: 8),
          Text(
            message,
            textAlign: TextAlign.center,
            style:
                AppTextStyles.body.copyWith(color: AppColors.textSecondary),
          ),
        ],
      ),
    );
  }
}
