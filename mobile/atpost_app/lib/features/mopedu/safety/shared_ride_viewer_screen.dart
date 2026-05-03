// Mopedu — public shared-ride viewer.
//
// Sprint 3. NO-AUTH route reached via deep link `/mopedu/share/:token`.
// The redacted view served by `GET /v1/rider/share/:token` carries:
//   * partner first name + photo
//   * vehicle number
//   * drop area (city/neighbourhood, never the full address)
//   * current location dot
//   * ETA seconds
//
// The viewer auto-refreshes every 10 seconds while the ride is in a
// non-terminal state (the `sharedRideProvider` schedules the timer
// internally). Once terminal, we stop polling and show the completion
// notice with a timestamp.
//
// PRIVACY: this screen never shows the customer's full name, the
// driver's last name, or any phone number. The map is a placeholder
// dot/pin until the maps integration ships.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class SharedRideViewerScreen extends ConsumerWidget {
  const SharedRideViewerScreen({super.key, required this.token});

  final String token;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final view = ref.watch(sharedRideProvider(token));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Shared ride', style: AppTextStyles.h2),
      ),
      body: view.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Text(
              'This share link is no longer active.\n$e',
              textAlign: TextAlign.center,
              style: AppTextStyles.bodySmall,
            ),
          ),
        ),
        data: (v) => _Body(view: v),
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({required this.view});

  final SharedRideView view;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
      children: [
        _StatusPill(view: view),
        const SizedBox(height: 12),
        _MapPlaceholder(view: view),
        const SizedBox(height: 12),
        _DriverCard(view: view),
        const SizedBox(height: 12),
        _DropCard(view: view),
        const SizedBox(height: 12),
        if (view.isTerminal)
          const _CompletedNotice()
        else
          _LiveTicker(view: view),
        const SizedBox(height: 16),
        const _Footer(),
      ],
    );
  }
}

class _StatusPill extends StatelessWidget {
  const _StatusPill({required this.view});
  final SharedRideView view;

  @override
  Widget build(BuildContext context) {
    final (label, color) = _styleFor(view.status);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(99),
        border: Border.all(color: color.withValues(alpha: 0.4)),
      ),
      child: Row(
        children: [
          Icon(Icons.circle, color: color, size: 10),
          const SizedBox(width: 8),
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
      case RideStatus.partnerAssigned:
        return ('Partner assigned', AppColors.statusSuccess);
      case RideStatus.partnerArriving:
        return ('Partner arriving', AppColors.postbookPrimary);
      case RideStatus.arrived:
        return ('Driver has arrived', AppColors.statusWarning);
      case RideStatus.otpVerified:
      case RideStatus.inProgress:
        return ('Ride in progress', AppColors.statusSuccess);
      case RideStatus.completed:
        return ('Ride completed', AppColors.statusSuccess);
      case RideStatus.expired:
      case RideStatus.failed:
        return ('Ride ended', AppColors.statusError);
      default:
        if (s.startsWith('cancelled_')) {
          return ('Ride cancelled', AppColors.statusError);
        }
        return (s, AppColors.textTertiary);
    }
  }
}

class _MapPlaceholder extends StatelessWidget {
  const _MapPlaceholder({required this.view});
  final SharedRideView view;

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
        children: [
          const Center(
            child: Icon(
              Icons.map_outlined,
              size: 48,
              color: AppColors.textTertiary,
            ),
          ),
          // Drop pin (top-right of the placeholder).
          Positioned(
            top: 16,
            right: 16,
            child: _LegendChip(
              icon: Icons.location_on,
              color: AppColors.statusError,
              label: view.dropArea.isEmpty ? 'Drop' : view.dropArea,
            ),
          ),
          // Current-location dot (centered).
          if (view.currentLocation != null)
            const Center(
              child: Icon(
                Icons.adjust,
                color: AppColors.posttubePrimary,
                size: 28,
              ),
            ),
          Positioned(
            bottom: 12,
            left: 12,
            child: _LegendChip(
              icon: Icons.adjust,
              color: AppColors.posttubePrimary,
              label: view.isTerminal
                  ? 'Last seen here'
                  : 'Live location',
            ),
          ),
        ],
      ),
    );
  }
}

class _LegendChip extends StatelessWidget {
  const _LegendChip({
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
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: AppColors.bgPrimary.withValues(alpha: 0.85),
        borderRadius: BorderRadius.circular(99),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 14, color: color),
          const SizedBox(width: 6),
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

class _DriverCard extends StatelessWidget {
  const _DriverCard({required this.view});
  final SharedRideView view;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          CircleAvatar(
            radius: 22,
            backgroundColor: AppColors.bgTertiary,
            backgroundImage: view.partnerPhotoUrl != null
                ? NetworkImage(view.partnerPhotoUrl!)
                : null,
            child: view.partnerPhotoUrl == null
                ? const Icon(Icons.person, color: AppColors.textTertiary)
                : null,
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Driver: ${view.partnerFirstName}',
                  style: AppTextStyles.label,
                ),
                if (view.vehicleNumber.isNotEmpty)
                  Text(
                    'Vehicle: ${view.vehicleNumber}',
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

class _DropCard extends StatelessWidget {
  const _DropCard({required this.view});
  final SharedRideView view;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          const Icon(Icons.location_on, color: AppColors.statusError),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Heading to', style: AppTextStyles.labelSmall),
                Text(
                  view.dropArea.isEmpty ? '—' : view.dropArea,
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.textPrimary,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _LiveTicker extends StatelessWidget {
  const _LiveTicker({required this.view});
  final SharedRideView view;

  @override
  Widget build(BuildContext context) {
    final eta = view.etaSeconds;
    final etaLabel = eta <= 0
        ? 'ETA updating...'
        : 'Arriving in ${(eta / 60).ceil()} min';
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.posttubePrimary.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(
          color: AppColors.posttubePrimary.withValues(alpha: 0.3),
        ),
      ),
      child: Row(
        children: [
          const Icon(
            Icons.access_time,
            color: AppColors.posttubePrimary,
            size: 18,
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              etaLabel,
              style: AppTextStyles.label.copyWith(
                color: AppColors.posttubePrimary,
              ),
            ),
          ),
          Text(
            'Auto-refreshing',
            style: AppTextStyles.labelSmall,
          ),
        ],
      ),
    );
  }
}

class _CompletedNotice extends StatelessWidget {
  const _CompletedNotice();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.statusSuccess.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(
          color: AppColors.statusSuccess.withValues(alpha: 0.3),
        ),
      ),
      child: Row(
        children: [
          const Icon(Icons.check_circle, color: AppColors.statusSuccess),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              'Ride completed at ${DateTime.now().toLocal().toString().split('.').first}.',
              style: AppTextStyles.label.copyWith(
                color: AppColors.statusSuccess,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _Footer extends StatelessWidget {
  const _Footer();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Text(
        'View shared by a Mopedu rider · secured one-time link',
        style: AppTextStyles.labelSmall,
      ),
    );
  }
}
