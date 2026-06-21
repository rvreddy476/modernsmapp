// Reviewer dashboard — entry point to the review flow. Shows the user's reviewer
// standing (status / tier / accuracy / earnings) and how much work is waiting,
// with a button into the review console. Non-reviewers see an opt-in card.
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/reviewer_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ReviewerDashboardScreen extends ConsumerStatefulWidget {
  const ReviewerDashboardScreen({super.key});

  @override
  ConsumerState<ReviewerDashboardScreen> createState() => _ReviewerDashboardScreenState();
}

class _ReviewerDashboardScreenState extends ConsumerState<ReviewerDashboardScreen> {
  ReviewerDashboard? _d;
  bool _loading = true;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    try {
      final d = await ref.read(reviewerRepositoryProvider).dashboard();
      if (mounted) setState(() => _d = d);
    } catch (_) {
      // leave _d null → shown as opt-in
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _refreshKyc() async {
    setState(() => _busy = true);
    try {
      final verified = await ref.read(reviewerRepositoryProvider).verifyKyc();
      await _load();
      if (mounted && !verified) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Not verified yet — complete identity verification first.')),
        );
      }
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(const SnackBar(content: Text('Could not check status. Try again.')));
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _optIn() async {
    setState(() => _busy = true);
    try {
      await ref.read(reviewerRepositoryProvider).optIn();
      await _load();
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(const SnackBar(content: Text('Could not opt in. Try again.')));
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(title: const Text('Reviewer'), backgroundColor: AppColors.bgPrimary),
      body: RefreshIndicator(
        onRefresh: _load,
        child: _loading
            ? const Center(child: CircularProgressIndicator())
            : ListView(
                padding: const EdgeInsets.all(20),
                children: (_d?.isReviewer ?? false) ? _reviewerView(_d!) : _optInView(_d),
              ),
      ),
    );
  }

  List<Widget> _optInView(ReviewerDashboard? d) {
    return [
      const SizedBox(height: 24),
      Icon(Icons.rate_review_outlined, size: 56, color: AppColors.postgramPrimary),
      const SizedBox(height: 16),
      Text('Become a reviewer', style: AppTextStyles.h2, textAlign: TextAlign.center),
      const SizedBox(height: 8),
      Text(
        'Help keep the platform clean. Watch short videos and either approve them '
        'or escalate to the admin team with notes. You earn for each review.',
        style: AppTextStyles.body.copyWith(color: AppColors.textSecondary),
        textAlign: TextAlign.center,
      ),
      if (d != null && d.pendingQueue > 0) ...[
        const SizedBox(height: 12),
        Text('${d.pendingQueue} videos waiting for review',
            style: AppTextStyles.labelSmall.copyWith(color: AppColors.textTertiary),
            textAlign: TextAlign.center),
      ],
      const SizedBox(height: 24),
      FilledButton(
        onPressed: _busy ? null : _optIn,
        style: FilledButton.styleFrom(padding: const EdgeInsets.symmetric(vertical: 14)),
        child: Text(_busy ? 'Joining…' : 'Opt in to review'),
      ),
    ];
  }

  List<Widget> _reviewerView(ReviewerDashboard d) {
    final earned = (d.lifetimeEarnedPaise / 100).toStringAsFixed(2);
    return [
      Row(
        children: [
          _StatCard(label: 'Tier', value: _cap(d.tier)),
          const SizedBox(width: 12),
          _StatCard(label: 'Accuracy', value: '${(d.accuracy * 100).round()}%'),
        ],
      ),
      const SizedBox(height: 12),
      Row(
        children: [
          _StatCard(label: 'Reviews', value: '${d.reviewsCompleted}'),
          const SizedBox(width: 12),
          _StatCard(label: 'Escalated', value: '${d.escalated}'),
        ],
      ),
      const SizedBox(height: 12),
      Row(
        children: [
          _StatCard(label: 'Earned', value: '₹$earned'),
          const SizedBox(width: 12),
          _StatCard(label: 'In queue', value: '${d.pendingQueue}'),
        ],
      ),
      if (d.status == 'suspended') ...[
        const SizedBox(height: 16),
        Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: const Color(0x33FF4D4F),
            borderRadius: BorderRadius.circular(10),
          ),
          child: Text('Your reviewer account is suspended.',
              style: AppTextStyles.body.copyWith(color: const Color(0xFFFF6B6B))),
        ),
      ],
      const SizedBox(height: 16),
      if (!d.kycVerified) ...[
        Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: AppColors.statusWarning.withValues(alpha: 0.5)),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(children: [
                Icon(Icons.verified_user_outlined, color: AppColors.statusWarning, size: 20),
                const SizedBox(width: 8),
                Text('Verify your identity', style: AppTextStyles.label),
              ]),
              const SizedBox(height: 6),
              Text(
                'Reviewing is paid work, so we verify identity (KYC) before you can '
                'start — required for payouts and to keep the system fair.',
                style: AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
              ),
              const SizedBox(height: 12),
              Row(children: [
                Expanded(
                  child: FilledButton(
                    onPressed: () => context.push('/wallet/kyc'),
                    style: FilledButton.styleFrom(padding: const EdgeInsets.symmetric(vertical: 12)),
                    child: const Text('Verify with DigiLocker'),
                  ),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: OutlinedButton(
                    onPressed: _busy ? null : _refreshKyc,
                    style: OutlinedButton.styleFrom(padding: const EdgeInsets.symmetric(vertical: 12)),
                    child: Text(_busy ? 'Checking…' : 'I\'ve verified'),
                  ),
                ),
              ]),
            ],
          ),
        ),
        const SizedBox(height: 16),
      ],
      FilledButton.icon(
        onPressed: (d.status == 'suspended' || !d.kycVerified) ? null : () => context.push('/reviewer'),
        icon: const Icon(Icons.play_arrow_rounded),
        label: Text(!d.kycVerified
            ? 'Verify identity to start'
            : (d.pendingQueue > 0 ? 'Start reviewing (${d.pendingQueue})' : 'Start reviewing')),
        style: FilledButton.styleFrom(padding: const EdgeInsets.symmetric(vertical: 14)),
      ),
    ];
  }

  String _cap(String s) => s.isEmpty ? s : s[0].toUpperCase() + s.substring(1);
}

class _StatCard extends StatelessWidget {
  const _StatCard({required this.label, required this.value});
  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Expanded(
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(value, style: AppTextStyles.h2.copyWith(fontSize: 22)),
            const SizedBox(height: 4),
            Text(label, style: AppTextStyles.labelSmall.copyWith(color: AppColors.textTertiary)),
          ],
        ),
      ),
    );
  }
}
