// Mopedu — booking-in-progress screen.
//
// Driven by `mopeduBookingNotifier` + a polling `rideProvider(id)`. The
// poll cadence (5s) is enforced inside the provider; this screen just
// listens and re-renders. When the ride hits `completed`, it routes to
// the ride summary.
//
// Map view is a placeholder card for Sprint 1; real maps land in
// Sprint 2 when `google_maps_flutter` is added to pubspec.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/features/mopedu/safety/share_ride_sheet.dart';
import 'package:atpost_app/features/mopedu/safety/sos_confirmation_sheet.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/mopedu_crash_breadcrumbs.dart';
import 'package:atpost_app/services/mopedu_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class BookingInProgressScreen extends ConsumerWidget {
  const BookingInProgressScreen({super.key, required this.rideId});

  final String rideId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncRide = ref.watch(rideProvider(rideId));

    // Sync notifier phase whenever a fresh ride lands.
    ref.listen<AsyncValue<Ride>>(rideProvider(rideId), (prev, next) {
      next.whenData((ride) {
        // Breadcrumb each state transition. We compare prev → next at
        // the status level so we don't crumb on every poll.
        final prevStatus = prev?.maybeWhen<String?>(
          data: (r) => r.status,
          orElse: () => null,
        );
        if (prevStatus != ride.status) {
          MopeduBreadcrumbs.bookingState(
            rideId: rideId,
            phase: ride.status,
          );
        }
        ref.read(mopeduBookingNotifier.notifier).onRideUpdate(ride);
        if (ride.status == RideStatus.completed) {
          // Telemetry on completion.
          ref.read(mopeduTelemetryProvider).mopeduRideCompleted(
                vehicleType: ride.vehicleType,
                cityId: ride.cityId,
                finalFarePaise: ride.finalFarePaise ?? ride.fareEstimatePaise,
              );
          ref.read(currentRideProvider.notifier).state = null;
          // Hop to summary.
          WidgetsBinding.instance.addPostFrameCallback((_) {
            if (context.mounted) {
              context.go('/mopedu/rides/$rideId');
            }
          });
        } else if (RideStatus.isTerminal(ride.status)) {
          ref.read(currentRideProvider.notifier).state = null;
        }
      });
    });

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Your ride', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () => context.canPop() ? context.pop() : context.go('/'),
        ),
        actions: [
          IconButton(
            tooltip: 'Safety',
            icon: const Icon(Icons.shield_outlined),
            onPressed: () => context.push('/mopedu/safety'),
          ),
        ],
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

class _Body extends ConsumerWidget {
  const _Body({required this.ride});

  final Ride ride;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
      children: [
        _StatusPill(status: ride.status),
        const SizedBox(height: 12),
        _MapPlaceholder(ride: ride),
        const SizedBox(height: 12),
        if (_showsPartner(ride.status) && ride.partnerName != null)
          _PartnerCard(ride: ride),
        if (_showsOtp(ride.status) && ride.otp != null)
          _OtpCard(otp: ride.otp!),
        const SizedBox(height: 12),
        _Actions(ride: ride),
      ],
    );
  }

  bool _showsPartner(String s) {
    return s == RideStatus.partnerAssigned ||
        s == RideStatus.partnerArriving ||
        s == RideStatus.arrived ||
        s == RideStatus.otpVerified ||
        s == RideStatus.inProgress;
  }

  bool _showsOtp(String s) {
    return s == RideStatus.partnerArriving || s == RideStatus.arrived;
  }
}

// ─── Status pill ──────────────────────────────────────────────────────

class _StatusPill extends StatelessWidget {
  const _StatusPill({required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    final (label, color) = _styleFor(status);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(99),
        border: Border.all(color: color.withValues(alpha: 0.4)),
      ),
      child: Row(
        children: [
          SizedBox(
            width: 12,
            height: 12,
            child: CircularProgressIndicator(
              strokeWidth: 2,
              valueColor: AlwaysStoppedAnimation(color),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              label,
              style: AppTextStyles.label.copyWith(color: color),
            ),
          ),
        ],
      ),
    );
  }

  (String, Color) _styleFor(String s) {
    switch (s) {
      case RideStatus.requested:
      case RideStatus.searchingPartner:
        return ('Searching for nearby partners...', AppColors.posttubePrimary);
      case RideStatus.partnerAssigned:
        return ('Partner assigned', AppColors.statusSuccess);
      case RideStatus.partnerArriving:
        return ('Partner arriving', AppColors.postbookPrimary);
      case RideStatus.arrived:
        return ('Partner has arrived', AppColors.statusWarning);
      case RideStatus.otpVerified:
      case RideStatus.inProgress:
        return ('Ride in progress', AppColors.statusSuccess);
      case RideStatus.completed:
        return ('Ride completed', AppColors.statusSuccess);
      case RideStatus.expired:
        return ('Search expired — please try again', AppColors.statusError);
      case RideStatus.failed:
        return ('Ride failed', AppColors.statusError);
      default:
        if (s.startsWith('cancelled_')) {
          return ('Ride cancelled', AppColors.statusError);
        }
        return (s, AppColors.textTertiary);
    }
  }
}

// ─── Map placeholder ──────────────────────────────────────────────────

class _MapPlaceholder extends StatelessWidget {
  const _MapPlaceholder({required this.ride});

  final Ride ride;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 240,
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Stack(
        alignment: Alignment.center,
        children: [
          Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              const Icon(
                Icons.map_outlined,
                size: 48,
                color: AppColors.textTertiary,
              ),
              const SizedBox(height: 8),
              Text('Map view (Sprint 2)', style: AppTextStyles.bodySmall),
              const SizedBox(height: 4),
              Text(
                ride.partnerName == null
                    ? 'Looking for a partner near you'
                    : 'Partner ${ride.partnerName} en route',
                style: AppTextStyles.labelSmall,
              ),
            ],
          ),
          Positioned(
            top: 12,
            left: 12,
            child: _MiniLegend(
              icon: Icons.trip_origin,
              color: AppColors.postbookPrimary,
              label: ride.pickup.displayName,
            ),
          ),
          Positioned(
            bottom: 12,
            right: 12,
            child: _MiniLegend(
              icon: Icons.location_on,
              color: AppColors.statusError,
              label: ride.drop.displayName,
            ),
          ),
        ],
      ),
    );
  }
}

class _MiniLegend extends StatelessWidget {
  const _MiniLegend({
    required this.icon,
    required this.color,
    required this.label,
  });

  final IconData icon;
  final Color color;
  final String label;

  @override
  Widget build(BuildContext context) {
    return Container(
      constraints: const BoxConstraints(maxWidth: 200),
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: AppColors.bgPrimary.withValues(alpha: 0.8),
        borderRadius: BorderRadius.circular(99),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 12, color: color),
          const SizedBox(width: 4),
          Flexible(
            child: Text(
              label,
              style: AppTextStyles.labelSmall,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
  }
}

// ─── Partner card ─────────────────────────────────────────────────────

class _PartnerCard extends StatelessWidget {
  const _PartnerCard({required this.ride});

  final Ride ride;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      margin: const EdgeInsets.only(bottom: 12),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          CircleAvatar(
            radius: 26,
            backgroundColor: AppColors.bgTertiary,
            backgroundImage: ride.partnerPhotoUrl != null
                ? NetworkImage(ride.partnerPhotoUrl!)
                : null,
            child: ride.partnerPhotoUrl == null
                ? const Icon(Icons.person, color: AppColors.textTertiary)
                : null,
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  ride.partnerName ?? 'Driver',
                  style: AppTextStyles.label,
                ),
                if (ride.partnerRating != null)
                  Row(
                    children: [
                      const Icon(
                        Icons.star,
                        size: 12,
                        color: AppColors.statusWarning,
                      ),
                      const SizedBox(width: 2),
                      Text(
                        ride.partnerRating!.toStringAsFixed(1),
                        style: AppTextStyles.labelSmall,
                      ),
                    ],
                  ),
                if (ride.vehicleNumber != null)
                  Text(
                    ride.vehicleNumber!,
                    style: AppTextStyles.bodySmall,
                  ),
              ],
            ),
          ),
          IconButton(
            tooltip: 'Call (masked)',
            icon: const Icon(Icons.phone, color: AppColors.postbookPrimary),
            onPressed: () => ScaffoldMessenger.of(context).showSnackBar(
              const SnackBar(content: Text('Masked call coming in v1.5')),
            ),
          ),
        ],
      ),
    );
  }
}

// ─── OTP card ─────────────────────────────────────────────────────────

class _OtpCard extends StatelessWidget {
  const _OtpCard({required this.otp});

  final String otp;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      onTap: () {
        Clipboard.setData(ClipboardData(text: otp));
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('OTP copied')),
        );
      },
      child: Container(
        padding: const EdgeInsets.all(14),
        margin: const EdgeInsets.only(bottom: 12),
        decoration: BoxDecoration(
          gradient: AppColors.ctaGradient,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        ),
        child: Row(
          children: [
            const Icon(Icons.lock, color: Colors.white),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    'Share this OTP with your driver',
                    style: AppTextStyles.label.copyWith(color: Colors.white),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    otp,
                    style: AppTextStyles.h1.copyWith(
                      color: Colors.white,
                      letterSpacing: 6,
                    ),
                  ),
                ],
              ),
            ),
            const Icon(Icons.copy, color: Colors.white, size: 18),
          ],
        ),
      ),
    );
  }
}

// ─── Actions ──────────────────────────────────────────────────────────

class _Actions extends ConsumerWidget {
  const _Actions({required this.ride});

  final Ride ride;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final s = ride.status;
    final children = <Widget>[];

    if (s == RideStatus.requested || s == RideStatus.searchingPartner) {
      children.add(
        _ActionButton(
          label: 'Cancel ride (free)',
          color: AppColors.statusError,
          onTap: () => _confirmCancel(
            context,
            ref,
            stage: MopeduCancelStage.searching,
            feeNotice: 'No fee will be charged.',
          ),
        ),
      );
    } else if (s == RideStatus.partnerAssigned ||
        s == RideStatus.partnerArriving) {
      children.add(
        _ActionButton(
          label: 'Cancel ride (₹15 fee)',
          color: AppColors.statusError,
          onTap: () => _confirmCancel(
            context,
            ref,
            stage: s == RideStatus.partnerAssigned
                ? MopeduCancelStage.assigned
                : MopeduCancelStage.arriving,
            feeNotice: 'A ₹15 cancellation fee will apply.',
          ),
        ),
      );
    } else if (s == RideStatus.arrived) {
      children.add(
        Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppColors.statusSuccess.withValues(alpha: 0.10),
            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            border: Border.all(
              color: AppColors.statusSuccess.withValues(alpha: 0.3),
            ),
          ),
          child: Row(
            children: [
              const Icon(
                Icons.directions_walk,
                color: AppColors.statusSuccess,
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Text(
                  "I'm in the cab — share the OTP above with the driver "
                  'to start the ride.',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.statusSuccess,
                  ),
                ),
              ),
            ],
          ),
        ),
      );
    } else if (s == RideStatus.otpVerified || s == RideStatus.inProgress) {
      children.addAll([
        _ActionButton(
          label: 'Share ride',
          color: AppColors.posttubePrimary,
          onTap: () => _handleShareRide(context, ride.id),
        ),
        const SizedBox(height: 8),
        _ActionButton(
          label: 'SOS',
          color: AppColors.statusError,
          onTap: () => _handleSOS(context, ride.id),
        ),
      ]);
    }

    if (children.isEmpty) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: children,
    );
  }

  Future<void> _confirmCancel(
    BuildContext context,
    WidgetRef ref, {
    required String stage,
    required String feeNotice,
  }) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Cancel ride?', style: AppTextStyles.h2),
        content: Text(feeNotice, style: AppTextStyles.body),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Keep ride'),
          ),
          TextButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: Text(
              'Cancel ride',
              style: TextStyle(color: AppColors.statusError),
            ),
          ),
        ],
      ),
    );
    if (ok != true) return;
    // Telemetry only — backend cancellation endpoint lands later in
    // Sprint 1 / Sprint 2. We surface a snackbar for v1.
    ref.read(mopeduBookingNotifier.notifier).noteCancelled(stage);
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Cancellation requested. Server hook lands shortly.'),
        ),
      );
    }
  }
}

// ─── Sprint 3 wiring: SOS + share-ride bottom sheets ──────────────────

Future<void> _handleSOS(BuildContext context, String rideId) async {
  await showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    isDismissible: false,
    enableDrag: false,
    backgroundColor: AppColors.bgSecondary,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
    ),
    builder: (_) => SosConfirmationSheet(rideId: rideId),
  );
}

Future<void> _handleShareRide(BuildContext context, String rideId) async {
  await showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    backgroundColor: AppColors.bgSecondary,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
    ),
    builder: (_) => ShareRideSheet(rideId: rideId),
  );
}

class _ActionButton extends StatelessWidget {
  const _ActionButton({
    required this.label,
    required this.color,
    required this.onTap,
  });

  final String label;
  final Color color;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: double.infinity,
      child: OutlinedButton(
        style: OutlinedButton.styleFrom(
          foregroundColor: color,
          side: BorderSide(color: color),
          padding: const EdgeInsets.symmetric(vertical: 14),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          ),
        ),
        onPressed: onTap,
        child: Text(label, style: AppTextStyles.label.copyWith(color: color)),
      ),
    );
  }
}
