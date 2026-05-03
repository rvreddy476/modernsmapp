// Ride request modal — Sprint 2.
//
// Full-screen overlay shown when an incoming `RideOffer` arrives. The
// dashboard listens to `incomingOffersProvider` and pushes this widget.
//
// Surface contract:
//   - Map placeholder (Google Maps not in pubspec → text/icon stub).
//   - Vehicle type, partial pickup address (city/area only — never the
//     full street; the backend already redacts), distance, ETA, fare,
//     customer rating.
//   - 15-second countdown timer auto-rejects on timeout.
//   - Two large buttons: ACCEPT (green) / REJECT (red, opens reason
//     picker).
//
// Telemetry: dashboard fires `mopedu.partner.offer.received` on push.
// This widget fires `accepted` / `rejected` from the actions notifier.

import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class RideRequestModal extends ConsumerStatefulWidget {
  const RideRequestModal({super.key, required this.offer});

  final RideOffer offer;

  @override
  ConsumerState<RideRequestModal> createState() => _RideRequestModalState();
}

class _RideRequestModalState extends ConsumerState<RideRequestModal> {
  Timer? _ticker;
  late Duration _remaining;

  @override
  void initState() {
    super.initState();
    _remaining = widget.offer.timeRemaining;
    _ticker = Timer.periodic(const Duration(seconds: 1), (_) {
      if (!mounted) return;
      setState(() {
        _remaining = widget.offer.timeRemaining;
      });
      if (_remaining == Duration.zero) {
        _ticker?.cancel();
        // Silent timeout — close, no telemetry beyond what the backend
        // already records via the offer expiry.
        if (mounted) Navigator.of(context).pop();
      }
    });
  }

  @override
  void dispose() {
    _ticker?.cancel();
    super.dispose();
  }

  Future<void> _onAccept() async {
    final ride = await ref
        .read(partnerOfferActionsProvider.notifier)
        .accept(widget.offer);
    if (!mounted) return;
    if (ride != null) {
      Navigator.of(context).pop();
      context.push('/mopedu/partner/rides/${ride.id}');
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not accept. Offer may have expired.')),
      );
    }
  }

  Future<void> _onReject() async {
    final reason = await showModalBottomSheet<String>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => const _ReasonSheet(),
    );
    if (reason == null) return;
    final ok = await ref
        .read(partnerOfferActionsProvider.notifier)
        .reject(widget.offer, reason);
    if (!mounted) return;
    if (ok) {
      Navigator.of(context).pop();
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not reject. Please retry.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final o = widget.offer;
    final action = ref.watch(partnerOfferActionsProvider);
    final secs = _remaining.inSeconds;
    final progress = (secs / 15).clamp(0.0, 1.0);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: Column(
          children: [
            // Map placeholder.
            Expanded(
              flex: 3,
              child: Container(
                width: double.infinity,
                color: AppColors.bgTertiary,
                alignment: Alignment.center,
                child: Column(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    const Icon(
                      Icons.map_outlined,
                      size: 60,
                      color: AppColors.textTertiary,
                    ),
                    const SizedBox(height: 8),
                    Text(
                      'Map view coming in Sprint 3',
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ),
            ),
            // Offer details.
            Expanded(
              flex: 4,
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    _CountdownBar(progress: progress, secs: secs),
                    const SizedBox(height: 12),
                    Row(
                      children: [
                        const Icon(
                          Icons.directions_car,
                          color: AppColors.posttubePrimary,
                        ),
                        const SizedBox(width: 8),
                        Text(
                          VehicleType.label(o.vehicleType),
                          style: AppTextStyles.h2,
                        ),
                        const Spacer(),
                        if (o.customerRating != null)
                          Row(
                            children: [
                              const Icon(
                                Icons.star,
                                size: 14,
                                color: AppColors.statusWarning,
                              ),
                              const SizedBox(width: 2),
                              Text(
                                o.customerRating!.toStringAsFixed(1),
                                style: AppTextStyles.label,
                              ),
                            ],
                          ),
                      ],
                    ),
                    const SizedBox(height: 12),
                    _AddrRow(
                      icon: Icons.trip_origin,
                      iconColor: AppColors.postbookPrimary,
                      label: 'Pickup',
                      addr: o.pickupAddress,
                    ),
                    const SizedBox(height: 6),
                    _AddrRow(
                      icon: Icons.location_on,
                      iconColor: AppColors.statusError,
                      label: 'Drop',
                      addr: o.dropAddress,
                    ),
                    const SizedBox(height: 16),
                    Row(
                      children: [
                        Expanded(
                          child: _StatBox(
                            label: 'Distance',
                            value: '${o.distanceKm.toStringAsFixed(1)} km',
                          ),
                        ),
                        const SizedBox(width: 8),
                        Expanded(
                          child: _StatBox(
                            label: 'ETA pickup',
                            value: '${(o.etaToPickupSeconds / 60).ceil()} min',
                          ),
                        ),
                        const SizedBox(width: 8),
                        Expanded(
                          child: _StatBox(
                            label: 'Fare',
                            value: formatRupees(o.fareEstimatePaise),
                          ),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ),
            // Action buttons.
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
              child: Row(
                children: [
                  Expanded(
                    child: SizedBox(
                      height: 56,
                      child: ElevatedButton(
                        style: ElevatedButton.styleFrom(
                          backgroundColor: AppColors.statusError,
                          foregroundColor: Colors.white,
                          shape: RoundedRectangleBorder(
                            borderRadius:
                                BorderRadius.circular(AppSpacing.radiusLarge),
                          ),
                        ),
                        onPressed: action.busy ? null : _onReject,
                        child: Text(
                          'REJECT',
                          style: AppTextStyles.h2.copyWith(color: Colors.white),
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    flex: 2,
                    child: SizedBox(
                      height: 56,
                      child: ElevatedButton(
                        style: ElevatedButton.styleFrom(
                          backgroundColor: AppColors.statusSuccess,
                          foregroundColor: Colors.white,
                          shape: RoundedRectangleBorder(
                            borderRadius:
                                BorderRadius.circular(AppSpacing.radiusLarge),
                          ),
                        ),
                        onPressed: action.busy ? null : _onAccept,
                        child: action.busy
                            ? const SizedBox(
                                width: 22,
                                height: 22,
                                child: CircularProgressIndicator(
                                  color: Colors.white,
                                  strokeWidth: 2,
                                ),
                              )
                            : Text(
                                'ACCEPT',
                                style: AppTextStyles.h2.copyWith(
                                  color: Colors.white,
                                ),
                              ),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _CountdownBar extends StatelessWidget {
  const _CountdownBar({required this.progress, required this.secs});

  final double progress;
  final int secs;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            const Icon(Icons.timer, size: 16, color: AppColors.statusWarning),
            const SizedBox(width: 6),
            Text('$secs s to decide', style: AppTextStyles.label),
          ],
        ),
        const SizedBox(height: 6),
        ClipRRect(
          borderRadius: BorderRadius.circular(99),
          child: LinearProgressIndicator(
            value: progress,
            minHeight: 6,
            backgroundColor: AppColors.bgTertiary,
            valueColor: AlwaysStoppedAnimation<Color>(
              progress > 0.4 ? AppColors.statusSuccess : AppColors.statusError,
            ),
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
              const SizedBox(height: 2),
              Text(
                addr.isEmpty ? '—' : addr,
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

class _StatBox extends StatelessWidget {
  const _StatBox({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label, style: AppTextStyles.labelSmall),
          const SizedBox(height: 4),
          Text(value, style: AppTextStyles.h3),
        ],
      ),
    );
  }
}

class _ReasonSheet extends StatelessWidget {
  const _ReasonSheet();

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
            Text('Why are you rejecting?', style: AppTextStyles.h2),
            const SizedBox(height: 12),
            for (final r in RideRejectReason.all)
              ListTile(
                leading: const Icon(
                  Icons.flag_outlined,
                  color: AppColors.textTertiary,
                ),
                title: Text(RideRejectReason.label(r), style: AppTextStyles.label),
                onTap: () => Navigator.of(context).pop(r),
              ),
          ],
        ),
      ),
    );
  }
}
