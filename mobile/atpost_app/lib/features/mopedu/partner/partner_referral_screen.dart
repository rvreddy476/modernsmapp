// Mopedu — Partner referral programme (Sprint 4).
//
// Hero card explaining the reward (a free week of Trial+ for both sides
// once the referred friend pays for their first plan), the partner's
// unique referral code, share buttons, and a list of referred users.
//
// Backend status: the v1 rider service does not yet expose a dedicated
// referrals endpoint. Until Sprint 5 ships it we derive the referral
// code from the first 6 chars of the partner id (uppercase) and surface
// the tracking list as an empty state with a TODO note.
//
// PRIVACY: telemetry only carries the channel bucket. The referral code
// itself derives from partner_id and is therefore PII-adjacent — we
// never pass it (or partner_id) into telemetry props. The banned-key
// guard in `MopeduTelemetry._bannedPropKeys` enforces this even if a
// future caller forgets the rule.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/mopedu_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PartnerReferralScreen extends ConsumerWidget {
  const PartnerReferralScreen({super.key});

  static const _channelWhatsApp = 'whatsapp';
  static const _channelCopy = 'copy';
  static const _channelSms = 'sms';

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncStats = ref.watch(partnerReferralStatsProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () => context.canPop() ? context.pop() : context.go('/'),
        ),
        title: Text('Refer a driver', style: AppTextStyles.h2),
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          ref.invalidate(partnerReferralStatsProvider);
          await ref.read(partnerReferralStatsProvider.future);
        },
        child: asyncStats.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, _) => ListView(
            children: [
              Padding(
                padding: const EdgeInsets.all(20),
                child: Text(
                  'Could not load referral details. Pull to refresh.',
                  style: AppTextStyles.body,
                ),
              ),
            ],
          ),
          data: (stats) => ListView(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 24),
            children: [
              const _HeroCard(),
              const SizedBox(height: 16),
              _CodeCard(stats: stats),
              const SizedBox(height: 16),
              _ShareRow(
                onShare: (channel) => _share(context, ref, stats, channel),
              ),
              const SizedBox(height: 20),
              _TrackingList(stats: stats),
              const SizedBox(height: 16),
              const _FinePrint(),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _share(
    BuildContext context,
    WidgetRef ref,
    ReferralStats stats,
    String channel,
  ) async {
    final code = stats.code;
    final message =
        'Join me on Mopedu! Use my code $code when you sign up — '
        'we both get a free week of Trial+ when you complete your first plan. '
        'Download: https://atpost.app/mopedu/partner';
    // Reuse the safety share-ride helper pattern: clipboard + snackbar.
    await Clipboard.setData(ClipboardData(text: message));
    if (!context.mounted) return;
    final label = switch (channel) {
      _channelWhatsApp => 'WhatsApp',
      _channelSms => 'SMS',
      _ => 'clipboard',
    };
    final body = channel == _channelCopy
        ? 'Referral message copied to clipboard.'
        : 'Message copied. Paste into $label to share.';
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(body)));
    // PRIVACY: only the channel bucket leaves the device.
    ref
        .read(mopeduTelemetryProvider)
        .mopeduPartnerReferralShared(channel: channel);
  }
}

class _HeroCard extends StatelessWidget {
  const _HeroCard();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(18),
      decoration: BoxDecoration(
        gradient: AppColors.ctaGradient,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.card_giftcard, color: Colors.white, size: 28),
          const SizedBox(height: 12),
          Text(
            'Refer a friend, both get a free week of Trial+',
            style: AppTextStyles.h2.copyWith(color: Colors.white),
          ),
          const SizedBox(height: 6),
          Text(
            'When your friend completes onboarding and pays for their first '
            'plan, you both get an extra 50 free leads.',
            style: AppTextStyles.body.copyWith(color: Colors.white),
          ),
        ],
      ),
    );
  }
}

class _CodeCard extends StatelessWidget {
  const _CodeCard({required this.stats});

  final ReferralStats stats;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Your referral code', style: AppTextStyles.labelSmall),
          const SizedBox(height: 6),
          Row(
            children: [
              Expanded(
                child: Text(
                  stats.code,
                  style: AppTextStyles.h1.copyWith(
                    color: AppColors.postbookPrimary,
                    fontSize: 30,
                    letterSpacing: 4,
                  ),
                ),
              ),
              IconButton(
                tooltip: 'Copy code',
                onPressed: () async {
                  await Clipboard.setData(ClipboardData(text: stats.code));
                  if (!context.mounted) return;
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(content: Text('Code copied.')),
                  );
                },
                icon: const Icon(Icons.copy, size: 20),
              ),
            ],
          ),
          const SizedBox(height: 4),
          Text(
            'Share this code with another driver. They enter it during '
            'onboarding so you both qualify for the bonus.',
            style: AppTextStyles.bodySmall,
          ),
        ],
      ),
    );
  }
}

class _ShareRow extends StatelessWidget {
  const _ShareRow({required this.onShare});

  final void Function(String channel) onShare;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(
          child: ElevatedButton.icon(
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.statusSuccess,
              foregroundColor: Colors.white,
              padding: const EdgeInsets.symmetric(vertical: 12),
            ),
            onPressed: () => onShare('whatsapp'),
            icon: const Icon(Icons.chat),
            label: const Text('WhatsApp'),
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: OutlinedButton.icon(
            style: OutlinedButton.styleFrom(
              padding: const EdgeInsets.symmetric(vertical: 12),
            ),
            onPressed: () => onShare('copy'),
            icon: const Icon(Icons.copy, size: 18),
            label: const Text('Copy link'),
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: ElevatedButton.icon(
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.posttubePrimary,
              foregroundColor: Colors.white,
              padding: const EdgeInsets.symmetric(vertical: 12),
            ),
            onPressed: () => onShare('sms'),
            icon: const Icon(Icons.sms),
            label: const Text('SMS'),
          ),
        ),
      ],
    );
  }
}

class _TrackingList extends StatelessWidget {
  const _TrackingList({required this.stats});

  final ReferralStats stats;

  @override
  Widget build(BuildContext context) {
    final pending = stats.pendingCount;
    final activated = stats.activatedCount;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text('Your referrals', style: AppTextStyles.h3),
              const Spacer(),
              _Pill(
                label: 'Pending: $pending',
                color: AppColors.statusWarning,
              ),
              const SizedBox(width: 6),
              _Pill(
                label: 'Activated: $activated',
                color: AppColors.statusSuccess,
              ),
            ],
          ),
          const SizedBox(height: 12),
          if (pending == 0 && activated == 0)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 6),
              child: Text(
                'No referrals yet. Share your code to get started.',
                style: AppTextStyles.bodySmall,
              ),
            )
          else ...[
            for (var i = 0; i < activated; i++)
              _ReferralRow(
                label: 'Driver ${i + 1}',
                status: 'Activated',
                color: AppColors.statusSuccess,
              ),
            for (var i = 0; i < pending; i++)
              _ReferralRow(
                label: 'Driver ${activated + i + 1}',
                status: 'Pending onboarding',
                color: AppColors.statusWarning,
              ),
          ],
          const SizedBox(height: 8),
          Text(
            'Detailed tracking lands once the referrals service ships '
            'in Sprint 5.',
            style: AppTextStyles.labelSmall,
          ),
        ],
      ),
    );
  }
}

class _ReferralRow extends StatelessWidget {
  const _ReferralRow({
    required this.label,
    required this.status,
    required this.color,
  });

  final String label;
  final String status;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Row(
        children: [
          CircleAvatar(
            radius: 14,
            backgroundColor: color.withValues(alpha: 0.18),
            child: Icon(Icons.person, size: 16, color: color),
          ),
          const SizedBox(width: 10),
          Expanded(child: Text(label, style: AppTextStyles.body)),
          Text(
            status,
            style: AppTextStyles.labelSmall.copyWith(color: color),
          ),
        ],
      ),
    );
  }
}

class _Pill extends StatelessWidget {
  const _Pill({required this.label, required this.color});
  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.18),
        borderRadius: BorderRadius.circular(99),
      ),
      child: Text(
        label,
        style: AppTextStyles.labelSmall.copyWith(color: color),
      ),
    );
  }
}

class _FinePrint extends StatelessWidget {
  const _FinePrint();

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: Text(
        'Bonus leads are credited within 24 hours of your friend paying '
        'for their first plan. One bonus per partner per cycle.',
        style: AppTextStyles.labelSmall,
      ),
    );
  }
}
