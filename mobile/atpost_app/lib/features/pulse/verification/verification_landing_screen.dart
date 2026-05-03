import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/features/pulse/verification/aadhaar_verification_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Sprint 4 — landing screen for the verification ladder. Shows the three
/// trust tiers (Phone -> Selfie -> Aadhaar) with a visual progress bar plus
/// CTAs for the two upgradable paths.
class VerificationLandingScreen extends ConsumerWidget {
  const VerificationLandingScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final profileAsync = ref.watch(_pulseTrustTierProvider);
    final tier = profileAsync.maybeWhen(
      data: (p) => p?.toLowerCase() ?? 'none',
      orElse: () => 'none',
    );

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Verification', style: AppTextStyles.h2),
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
              _Header(tier: tier),
              const SizedBox(height: 18),
              _Ladder(currentTier: tier),
              const SizedBox(height: 18),
              const _DisclosureBanner(),
              const SizedBox(height: 22),
              FilledButton(
                onPressed: () => context.push('/pulse/verification/aadhaar'),
                style: FilledButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  minimumSize: const Size.fromHeight(52),
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                  ),
                ),
                child: Text('Verify with Aadhaar',
                    style: AppTextStyles.h3.copyWith(color: Colors.white)),
              ),
              const SizedBox(height: 12),
              FilledButton.tonal(
                onPressed: () => context.push('/pulse/verification/selfie'),
                style: FilledButton.styleFrom(
                  minimumSize: const Size.fromHeight(52),
                  backgroundColor: AppColors.bgCard,
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                  ),
                ),
                child: Text('Verify with Selfie',
                    style: AppTextStyles.h3),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.tier});

  final String tier;

  @override
  Widget build(BuildContext context) {
    final label = switch (tier) {
      'aadhaar' => 'Aadhaar Verified',
      'selfie' => 'Selfie Verified',
      'phone' => 'Phone Verified',
      _ => 'Not yet verified',
    };
    return Container(
      padding: const EdgeInsets.all(18),
      decoration: BoxDecoration(
        gradient: AppColors.posttubeGradient,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Trust ladder', style: AppTextStyles.labelSmall),
          const SizedBox(height: 6),
          Text('You are: $label',
              style: AppTextStyles.h2.copyWith(color: Colors.white)),
          const SizedBox(height: 6),
          Text(
            'Each step unlocks more of Pulse and helps people meet you with '
            'confidence.',
            style: AppTextStyles.bodySmall.copyWith(
                color: Colors.white.withAlpha(220)),
          ),
        ],
      ),
    );
  }
}

class _Ladder extends StatelessWidget {
  const _Ladder({required this.currentTier});

  final String currentTier;

  static const List<String> _order = ['phone', 'selfie', 'aadhaar'];

  int _rank(String tier) {
    final i = _order.indexOf(tier);
    return i < 0 ? -1 : i;
  }

  @override
  Widget build(BuildContext context) {
    final rank = _rank(currentTier);
    final progress = rank < 0 ? 0.0 : (rank + 1) / _order.length;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _ProgressBar(value: progress),
        const SizedBox(height: 12),
        _TierTile(
          icon: Icons.phone_iphone,
          title: 'Phone',
          subtitle: 'Done at signup. Confirms a real Indian number.',
          status: rank >= 0
              ? _TileStatus.completed
              : _TileStatus.pending,
        ),
        const SizedBox(height: 8),
        _TierTile(
          icon: Icons.face,
          title: 'Selfie',
          subtitle: 'Match a live selfie with your profile photos.',
          status: rank >= 1
              ? _TileStatus.completed
              : (rank >= 0 ? _TileStatus.next : _TileStatus.pending),
        ),
        const SizedBox(height: 8),
        _TierTile(
          icon: Icons.shield_outlined,
          title: 'Aadhaar',
          subtitle:
              'DigiLocker verification token. Unlocks Marriage pool.',
          status: rank >= 2
              ? _TileStatus.completed
              : (rank >= 1 ? _TileStatus.next : _TileStatus.pending),
        ),
      ],
    );
  }
}

enum _TileStatus { pending, next, completed }

class _TierTile extends StatelessWidget {
  const _TierTile({
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.status,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final _TileStatus status;

  @override
  Widget build(BuildContext context) {
    final color = switch (status) {
      _TileStatus.completed => AppColors.statusSuccess,
      _TileStatus.next => AppColors.postbookPrimary,
      _TileStatus.pending => AppColors.textMuted,
    };
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: color.withAlpha(80)),
      ),
      child: Row(
        children: [
          Icon(icon, color: color, size: 22),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: AppTextStyles.h3),
                const SizedBox(height: 2),
                Text(subtitle, style: AppTextStyles.bodySmall),
              ],
            ),
          ),
          Icon(
            status == _TileStatus.completed
                ? Icons.check_circle
                : status == _TileStatus.next
                    ? Icons.east
                    : Icons.lock_outline,
            color: color,
            size: 22,
          ),
        ],
      ),
    );
  }
}

class _ProgressBar extends StatelessWidget {
  const _ProgressBar({required this.value});

  final double value;

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
      child: LinearProgressIndicator(
        value: value.clamp(0.0, 1.0),
        minHeight: 8,
        backgroundColor: AppColors.bgCard,
        valueColor: const AlwaysStoppedAnimation<Color>(
          AppColors.posttubePrimary,
        ),
      ),
    );
  }
}

class _DisclosureBanner extends StatelessWidget {
  const _DisclosureBanner();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.shield_outlined,
              color: AppColors.posttubePrimary, size: 20),
          const SizedBox(width: 10),
          Expanded(
            child: Text(kAadhaarDpdpDisclosure,
                style: AppTextStyles.bodySmall),
          ),
        ],
      ),
    );
  }
}

/// Tiny helper provider that pulls trust tier off the dating profile. Cached
/// here (not in `pulse_providers.dart`) because no other surface needs it
/// today.
final _pulseTrustTierProvider =
    FutureProvider.autoDispose<String?>((ref) async {
  final repo = ref.watch(pulseRepositoryProvider);
  final profile = await repo.getProfile();
  if (profile == null) return null;
  // Profile model doesn't yet expose trustTier directly; the field rides on
  // the backend payload as `trust_tier`. We stash it on a known place when
  // it lands; for now return null and the screen falls back to "Not yet
  // verified".
  return null;
});
