// Mopedu — ride summary / receipt.
//
// Loads a ride via `rideProvider(id)` (which has already terminal-checked
// itself, so no polling here). Receipt-style card + 5-star rating
// stub + "book again" CTA back to the home.
//
// PRIVACY: the rating call goes through `MopeduRepository.rateRide`,
// which today returns false-on-error so the UX always shows a "Thanks"
// snackbar. Rating endpoint lands in Sprint 2.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/data/repositories/mopedu_repository.dart';
import 'package:atpost_app/features/mopedu/safety/complaint_sheet.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class RideSummaryScreen extends ConsumerWidget {
  const RideSummaryScreen({super.key, required this.rideId});

  final String rideId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncRide = ref.watch(rideProvider(rideId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Ride summary', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () =>
              context.canPop() ? context.pop() : context.go('/mopedu'),
        ),
      ),
      body: asyncRide.when(
        data: (ride) => _Body(ride: ride),
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Text(
              'Could not load ride.\n$e',
              style: AppTextStyles.bodySmall,
              textAlign: TextAlign.center,
            ),
          ),
        ),
      ),
    );
  }
}

class _Body extends ConsumerStatefulWidget {
  const _Body({required this.ride});

  final Ride ride;

  @override
  ConsumerState<_Body> createState() => _BodyState();
}

class _BodyState extends ConsumerState<_Body> {
  int _stars = 0;
  final _commentCtl = TextEditingController();
  bool _ratingSubmitted = false;

  @override
  void dispose() {
    _commentCtl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final ride = widget.ride;
    final fareLabel = formatRupees(
      ride.finalFarePaise ?? ride.fareEstimatePaise,
    );

    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
      children: [
        _ReceiptCard(ride: ride, fareLabel: fareLabel),
        const SizedBox(height: 16),
        _RatingCard(
          stars: _stars,
          submitted: _ratingSubmitted,
          commentCtl: _commentCtl,
          onStar: (n) => setState(() => _stars = n),
          onSubmit: _submitRating,
        ),
        const SizedBox(height: 16),
        _ReceiptActions(rideId: ride.id),
        const SizedBox(height: 16),
        const _BookAgain(),
        const SizedBox(height: 12),
        _ComplaintLink(rideId: ride.id),
      ],
    );
  }

  Future<void> _submitRating() async {
    if (_stars == 0) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Pick a star count first')),
      );
      return;
    }
    final repo = ref.read(mopeduRepositoryProvider);
    // Best-effort. Today's S2 endpoint is a TODO; the repository swallows
    // errors and returns false. We always thank the user either way.
    await repo.rateRide(
      rideId: widget.ride.id,
      stars: _stars,
      comment: _commentCtl.text.trim(),
    );
    if (!mounted) return;
    setState(() => _ratingSubmitted = true);
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Thanks for rating!')),
    );
  }
}

// ─── Receipt card ─────────────────────────────────────────────────────

class _ReceiptCard extends StatelessWidget {
  const _ReceiptCard({required this.ride, required this.fareLabel});

  final Ride ride;
  final String fareLabel;

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
          Row(
            children: [
              const Icon(
                Icons.receipt_long,
                color: AppColors.postbookPrimary,
              ),
              const SizedBox(width: 8),
              Text('Receipt', style: AppTextStyles.h2),
              const Spacer(),
              Text(fareLabel, style: AppTextStyles.h2),
            ],
          ),
          const SizedBox(height: 12),
          _Row(label: 'Pickup', value: ride.pickup.displayName),
          _Row(label: 'Drop', value: ride.drop.displayName),
          const Divider(color: AppColors.borderSubtle, height: 24),
          _Row(label: 'Vehicle', value: VehicleType.label(ride.vehicleType)),
          if (ride.partnerName != null)
            _Row(label: 'Driver', value: ride.partnerName!),
          if (ride.vehicleNumber != null)
            _Row(label: 'Vehicle no.', value: ride.vehicleNumber!),
          _Row(label: 'Payment', value: ride.paymentMethod),
          if (ride.completedAt != null)
            _Row(
              label: 'Completed',
              value: ride.completedAt!.toLocal().toString().split('.').first,
            ),
          const Divider(color: AppColors.borderSubtle, height: 24),
          _Row(label: 'Total fare', value: fareLabel, isBold: true),
        ],
      ),
    );
  }
}

class _Row extends StatelessWidget {
  const _Row({
    required this.label,
    required this.value,
    this.isBold = false,
  });

  final String label;
  final String value;
  final bool isBold;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 110,
            child: Text(label, style: AppTextStyles.bodySmall),
          ),
          Expanded(
            child: Text(
              value,
              style: isBold
                  ? AppTextStyles.h3
                  : AppTextStyles.label.copyWith(
                      color: AppColors.textPrimary,
                    ),
            ),
          ),
        ],
      ),
    );
  }
}

// ─── Rating card ──────────────────────────────────────────────────────

class _RatingCard extends StatelessWidget {
  const _RatingCard({
    required this.stars,
    required this.submitted,
    required this.commentCtl,
    required this.onStar,
    required this.onSubmit,
  });

  final int stars;
  final bool submitted;
  final TextEditingController commentCtl;
  final ValueChanged<int> onStar;
  final VoidCallback onSubmit;

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
          Text('Rate your driver', style: AppTextStyles.h3),
          const SizedBox(height: 8),
          Row(
            children: [
              for (var i = 1; i <= 5; i++)
                IconButton(
                  onPressed: submitted ? null : () => onStar(i),
                  icon: Icon(
                    i <= stars ? Icons.star : Icons.star_border,
                    color: AppColors.statusWarning,
                    size: 28,
                  ),
                ),
            ],
          ),
          TextField(
            controller: commentCtl,
            enabled: !submitted,
            maxLines: 1,
            style: AppTextStyles.body,
            decoration: InputDecoration(
              hintText: 'Optional one-line comment',
              hintStyle: AppTextStyles.bodySmall,
              filled: true,
              fillColor: AppColors.bgTertiary,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                borderSide: BorderSide.none,
              ),
              contentPadding: const EdgeInsets.symmetric(
                horizontal: 12,
                vertical: 12,
              ),
            ),
          ),
          const SizedBox(height: 10),
          SizedBox(
            width: double.infinity,
            child: ElevatedButton(
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                foregroundColor: Colors.white,
              ),
              onPressed: submitted ? null : onSubmit,
              child: Text(submitted ? 'Submitted' : 'Submit rating'),
            ),
          ),
        ],
      ),
    );
  }
}

// ─── Receipt actions ──────────────────────────────────────────────────

class _ReceiptActions extends StatelessWidget {
  const _ReceiptActions({required this.rideId});

  final String rideId;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(
          child: OutlinedButton.icon(
            onPressed: () => ScaffoldMessenger.of(context).showSnackBar(
              const SnackBar(
                content: Text('PDF receipts ship in Sprint 2'),
              ),
            ),
            icon: const Icon(Icons.download),
            label: const Text('Get receipt (PDF)'),
          ),
        ),
      ],
    );
  }
}

class _BookAgain extends StatelessWidget {
  const _BookAgain();

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: double.infinity,
      height: 48,
      child: ElevatedButton.icon(
        style: ElevatedButton.styleFrom(
          backgroundColor: AppColors.postbookPrimary,
          foregroundColor: Colors.white,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          ),
        ),
        onPressed: () => context.go('/mopedu'),
        icon: const Icon(Icons.add),
        label: const Text('Book again'),
      ),
    );
  }
}

class _ComplaintLink extends StatelessWidget {
  const _ComplaintLink({required this.rideId});

  final String rideId;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: TextButton.icon(
        onPressed: () => _openComplaintSheet(context, rideId),
        icon: const Icon(Icons.flag, size: 14),
        label: const Text('Issue with this ride?'),
      ),
    );
  }
}

Future<void> _openComplaintSheet(BuildContext context, String rideId) async {
  await showModalBottomSheet<bool>(
    context: context,
    isScrollControlled: true,
    backgroundColor: AppColors.bgSecondary,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
    ),
    builder: (_) => ComplaintSheet(rideId: rideId),
  );
}
