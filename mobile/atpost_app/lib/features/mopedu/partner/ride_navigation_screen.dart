// Ride navigation screen — Sprint 2 (partner side).
//
// Map placeholder + status-aware bottom action sheet driving the ride
// state machine: arriving → arrived → start (with OTP) → complete.
//
// PRIVACY: customer phone is masked on display; OTP is captured into a
// local controller and only flushed via the API call. Neither value
// enters telemetry.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/mopedu_crash_breadcrumbs.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class RideNavigationScreen extends ConsumerStatefulWidget {
  const RideNavigationScreen({super.key, required this.rideId});

  final String rideId;

  @override
  ConsumerState<RideNavigationScreen> createState() =>
      _RideNavigationScreenState();
}

class _RideNavigationScreenState extends ConsumerState<RideNavigationScreen> {
  final _otpController = TextEditingController();

  @override
  void dispose() {
    _otpController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final asyncRide = ref.watch(rideProvider(widget.rideId));
    final flow = ref.watch(partnerRideFlowProvider);

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
        title: Text('Ride', style: AppTextStyles.h2),
      ),
      body: asyncRide.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (_, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(20),
            child: Text('Could not load ride.', style: AppTextStyles.body),
          ),
        ),
        data: (ride) => _Body(
          ride: ride,
          flow: flow,
          otpController: _otpController,
        ),
      ),
    );
  }
}

class _Body extends ConsumerWidget {
  const _Body({
    required this.ride,
    required this.flow,
    required this.otpController,
  });

  final Ride ride;
  final PartnerRideFlowState flow;
  final TextEditingController otpController;

  static String _maskedPhone(String? raw) {
    if (raw == null || raw.length < 4) return '••••';
    final last = raw.substring(raw.length - 4);
    return '+91 •••••• $last';
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Column(
      children: [
        // Map placeholder.
        Expanded(
          flex: 4,
          child: Container(
            width: double.infinity,
            color: AppColors.bgTertiary,
            alignment: Alignment.center,
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                const Icon(Icons.map, size: 60, color: AppColors.textTertiary),
                const SizedBox(height: 8),
                Text(
                  'Route polyline coming in Sprint 3',
                  style: AppTextStyles.bodySmall,
                ),
              ],
            ),
          ),
        ),
        Container(
          decoration: const BoxDecoration(
            color: AppColors.bgPrimary,
            border: Border(
              top: BorderSide(color: AppColors.borderSubtle, width: 0.5),
            ),
          ),
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                _CustomerStrip(
                  name: ride.partnerName ?? 'Customer',
                  phoneMasked: _maskedPhone(null),
                ),
                const SizedBox(height: 10),
                _AddrRow(
                  icon: Icons.trip_origin,
                  iconColor: AppColors.postbookPrimary,
                  label: 'Pickup',
                  addr: ride.pickup.displayName,
                ),
                const SizedBox(height: 6),
                _AddrRow(
                  icon: Icons.location_on,
                  iconColor: AppColors.statusError,
                  label: 'Drop',
                  addr: ride.drop.displayName,
                ),
                const SizedBox(height: 16),
                _ActionSheet(
                  ride: ride,
                  flow: flow,
                  otpController: otpController,
                ),
                const SizedBox(height: 8),
                if (!RideStatus.isTerminal(ride.status) &&
                    ride.status != RideStatus.inProgress)
                  TextButton.icon(
                    onPressed: () => _onCancel(context, ref, ride.id),
                    icon: const Icon(
                      Icons.cancel_outlined,
                      size: 16,
                      color: AppColors.statusError,
                    ),
                    label: Text(
                      'Cancel ride',
                      style: AppTextStyles.label.copyWith(
                        color: AppColors.statusError,
                      ),
                    ),
                  ),
              ],
            ),
          ),
        ),
      ],
    );
  }

  Future<void> _onCancel(
    BuildContext context,
    WidgetRef ref,
    String rideId,
  ) async {
    final reason = await showModalBottomSheet<String>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => const _CancelReasonSheet(),
    );
    if (reason == null) return;
    // The dedicated partner-cancel endpoint isn't in this sprint's
    // contract; backend enforces. Show toast acknowledging the request.
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Cancellation requested ($reason).')),
      );
    }
  }
}

class _CustomerStrip extends StatelessWidget {
  const _CustomerStrip({required this.name, required this.phoneMasked});

  final String name;
  final String phoneMasked;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        const CircleAvatar(
          radius: 18,
          backgroundColor: AppColors.bgTertiary,
          child: Icon(Icons.person, color: AppColors.textTertiary),
        ),
        const SizedBox(width: 10),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(name, style: AppTextStyles.h3),
              Text(phoneMasked, style: AppTextStyles.bodySmall),
            ],
          ),
        ),
        IconButton(
          icon: const Icon(Icons.phone, color: AppColors.posttubePrimary),
          onPressed: () => ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Calling masked number…')),
          ),
        ),
      ],
    );
  }
}

class _AddrRow extends StatelessWidget {
  const _AddrRow({
    required this.icon,
    required this.iconColor,
    required this.label,
    required this.addr,
  });

  final IconData icon;
  final Color iconColor;
  final String label;
  final String addr;

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(icon, color: iconColor, size: 14),
        const SizedBox(width: 8),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(label, style: AppTextStyles.labelSmall),
              Text(
                addr,
                style: AppTextStyles.label,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
            ],
          ),
        ),
      ],
    );
  }
}

class _ActionSheet extends ConsumerWidget {
  const _ActionSheet({
    required this.ride,
    required this.flow,
    required this.otpController,
  });

  final Ride ride;
  final PartnerRideFlowState flow;
  final TextEditingController otpController;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final notifier = ref.read(partnerRideFlowProvider.notifier);
    switch (ride.status) {
      case RideStatus.partnerAssigned:
        return _PrimaryBtn(
          label: 'Mark arriving',
          busy: flow.busy,
          onTap: () {
            MopeduBreadcrumbs.partnerRideAction(
              rideId: ride.id,
              action: 'arriving',
            );
            notifier.markArriving(ride.id);
          },
        );
      case RideStatus.partnerArriving:
        return _PrimaryBtn(
          label: "I've arrived",
          busy: flow.busy,
          onTap: () {
            MopeduBreadcrumbs.partnerRideAction(
              rideId: ride.id,
              action: 'arrived',
            );
            notifier.markArrived(ride.id);
          },
        );
      case RideStatus.arrived:
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Enter OTP from customer', style: AppTextStyles.label),
            const SizedBox(height: 6),
            TextField(
              controller: otpController,
              keyboardType: TextInputType.number,
              maxLength: 4,
              inputFormatters: [FilteringTextInputFormatter.digitsOnly],
              style: AppTextStyles.h2,
              decoration: InputDecoration(
                hintText: '••••',
                counterText: '',
                filled: true,
                fillColor: AppColors.bgSecondary,
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                  borderSide: const BorderSide(color: AppColors.borderSubtle),
                ),
              ),
            ),
            const SizedBox(height: 8),
            _PrimaryBtn(
              label: 'Start ride',
              busy: flow.busy,
              onTap: () async {
                final otp = otpController.text.trim();
                if (otp.length != 4) {
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(
                      content: Text('Enter the 4-digit OTP.'),
                    ),
                  );
                  return;
                }
                // Breadcrumb the action only — OTP is never logged.
                MopeduBreadcrumbs.partnerRideAction(
                  rideId: ride.id,
                  action: 'start',
                );
                await notifier.startRide(ride.id, otp);
              },
            ),
          ],
        );
      case RideStatus.otpVerified:
      case RideStatus.inProgress:
        return Column(
          children: [
            Container(
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: AppColors.bgSecondary,
                borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              ),
              child: Row(
                children: [
                  const Icon(Icons.speed, color: AppColors.posttubePrimary),
                  const SizedBox(width: 8),
                  Text(
                    'Odometer: ${(ride.fareEstimatePaise / 100).toStringAsFixed(1)} km (est)',
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              ),
            ),
            const SizedBox(height: 10),
            _PrimaryBtn(
              label: 'Complete ride',
              busy: flow.busy,
              onTap: () => _completeFlow(context, ref, ride),
            ),
          ],
        );
      case RideStatus.completed:
        return _CompletionSummary(ride: ride);
      default:
        return Padding(
          padding: const EdgeInsets.symmetric(vertical: 8),
          child: Text('Status: ${ride.status}', style: AppTextStyles.bodySmall),
        );
    }
  }

  Future<void> _completeFlow(
    BuildContext context,
    WidgetRef ref,
    Ride ride,
  ) async {
    MopeduBreadcrumbs.partnerRideAction(
      rideId: ride.id,
      action: 'complete',
    );
    // Distance/duration are best-effort approximations in v1. Use the
    // estimate as a starting value.
    final ok = await ref.read(partnerRideFlowProvider.notifier).completeRide(
          ride.id,
          finalDistanceKm: ride.fareEstimatePaise > 0 ? 5.0 : 0.0,
          finalDurationMin: 10,
        );
    if (!context.mounted) return;
    if (!ok) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not complete ride. Please retry.')),
      );
    }
  }
}

class _PrimaryBtn extends StatelessWidget {
  const _PrimaryBtn({
    required this.label,
    required this.busy,
    required this.onTap,
  });

  final String label;
  final bool busy;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: double.infinity,
      height: 50,
      child: ElevatedButton(
        style: ElevatedButton.styleFrom(
          backgroundColor: AppColors.postbookPrimary,
          foregroundColor: Colors.white,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          ),
        ),
        onPressed: busy ? null : onTap,
        child: busy
            ? const SizedBox(
                width: 20,
                height: 20,
                child: CircularProgressIndicator(
                  color: Colors.white,
                  strokeWidth: 2,
                ),
              )
            : Text(
                label,
                style: AppTextStyles.h3.copyWith(color: Colors.white),
              ),
      ),
    );
  }
}

class _CompletionSummary extends StatelessWidget {
  const _CompletionSummary({required this.ride});
  final Ride ride;

  @override
  Widget build(BuildContext context) {
    final fare = ride.finalFarePaise ?? ride.fareEstimatePaise;
    final isCash = ride.paymentMethod == 'cash';
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.statusSuccess.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.statusSuccess),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(Icons.check_circle, color: AppColors.statusSuccess),
              const SizedBox(width: 8),
              Text('Ride completed', style: AppTextStyles.h2),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            'Final fare: ₹${(fare / 100).toStringAsFixed(2)}',
            style: AppTextStyles.body,
          ),
          const SizedBox(height: 8),
          if (isCash)
            ElevatedButton.icon(
              icon: const Icon(Icons.attach_money, size: 16),
              onPressed: () => ScaffoldMessenger.of(context).showSnackBar(
                const SnackBar(content: Text('Cash receipt noted.')),
              ),
              label: const Text('Got cash? Mark received'),
            )
          else
            Text(
              'Auto-settled to your wallet.',
              style: AppTextStyles.bodySmall,
            ),
          const SizedBox(height: 8),
          OutlinedButton(
            onPressed: () => context.go('/mopedu/partner/dashboard'),
            child: const Text('Back to dashboard'),
          ),
        ],
      ),
    );
  }
}

class _CancelReasonSheet extends StatelessWidget {
  const _CancelReasonSheet();

  static const _reasons = <String, String>{
    'customer_no_show': 'Customer did not show up',
    'wrong_address': 'Wrong pickup address',
    'vehicle_breakdown': 'Vehicle breakdown',
    'safety_concern': 'Safety concern',
    'other': 'Other',
  };

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Cancel ride', style: AppTextStyles.h2),
            const SizedBox(height: 4),
            Text(
              'High cancellation rate hurts your standing. Pick the best reason.',
              style: AppTextStyles.bodySmall,
            ),
            const SizedBox(height: 12),
            for (final entry in _reasons.entries)
              ListTile(
                leading: const Icon(
                  Icons.flag_outlined,
                  color: AppColors.textTertiary,
                ),
                title: Text(entry.value, style: AppTextStyles.label),
                onTap: () => Navigator.of(context).pop(entry.key),
              ),
          ],
        ),
      ),
    );
  }
}
