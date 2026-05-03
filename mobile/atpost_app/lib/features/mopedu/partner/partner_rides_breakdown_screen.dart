// Mopedu — Partner ride-by-ride breakdown (Sprint 4).
//
// Reachable from the earnings screen "View ride-by-ride" link. Lists the
// partner's recent completed rides with fare + a "₹0 commission" affordance
// (the partner's flat plan covers commission — surfacing this is part of
// the value-prop polish for Sprint 4).
//
// Data: reuses `myRidesProvider` because the v1 backend doesn't yet split
// the partner-side ride history endpoint from the customer one. We filter
// to terminal rides client-side and render the same surface for both
// Today / Week / Month.
//
// PRIVACY: telemetry is silent on this screen — fares are displayed via
// `formatRupees` but never re-emitted. Tapping a row opens the existing
// ride-summary screen.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PartnerRidesBreakdownScreen extends ConsumerWidget {
  const PartnerRidesBreakdownScreen({super.key, this.period = 'week'});

  /// `today | week | month` — purely a label for the header, the ride list
  /// itself is server-paged.
  final String period;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncRides =
        ref.watch(myRidesProvider(const MyRidesQuery(limit: 50)));
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
        title: Text('Ride-by-ride', style: AppTextStyles.h2),
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          ref.invalidate(myRidesProvider(const MyRidesQuery(limit: 50)));
          await ref
              .read(myRidesProvider(const MyRidesQuery(limit: 50)).future);
        },
        child: asyncRides.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, _) => ListView(
            children: [
              Padding(
                padding: const EdgeInsets.all(20),
                child: Text(
                  'Could not load rides. Pull to refresh.',
                  style: AppTextStyles.body,
                ),
              ),
            ],
          ),
          data: (page) {
            final rides = page.items
                .where((r) => r.status == RideStatus.completed)
                .toList();
            if (rides.isEmpty) {
              return ListView(
                children: [
                  Padding(
                    padding: const EdgeInsets.all(24),
                    child: Center(
                      child: Text(
                        'No completed rides for this period yet.',
                        style: AppTextStyles.body,
                      ),
                    ),
                  ),
                ],
              );
            }
            return ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
              itemCount: rides.length + 1,
              separatorBuilder: (_, _) => const SizedBox(height: 8),
              itemBuilder: (context, i) {
                if (i == 0) return _Header(period: period, count: rides.length);
                return _RideTile(ride: rides[i - 1]);
              },
            );
          },
        ),
      ),
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.period, required this.count});

  final String period;
  final int count;

  String get _label {
    switch (period) {
      case 'today':
        return 'Today';
      case 'month':
        return 'This month';
      case 'week':
      default:
        return 'This week';
    }
  }

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
          const Icon(Icons.directions_car, color: AppColors.posttubePrimary),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(_label, style: AppTextStyles.h3),
                const SizedBox(height: 2),
                Text(
                  '$count completed rides · 0% commission, kept by you.',
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

class _RideTile extends StatelessWidget {
  const _RideTile({required this.ride});

  final Ride ride;

  String _fmtTs(DateTime? t) {
    if (t == null) return '';
    final h = t.hour.toString().padLeft(2, '0');
    final m = t.minute.toString().padLeft(2, '0');
    return '${t.day}/${t.month}  $h:$m';
  }

  @override
  Widget build(BuildContext context) {
    final fare = ride.finalFarePaise ?? ride.fareEstimatePaise;
    final pickup = ride.pickup.displayName;
    final drop = ride.drop.displayName;
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      onTap: () => context.push('/mopedu/rides/${ride.id}'),
      child: Container(
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
                Expanded(
                  child: Text(
                    '$pickup → $drop',
                    style: AppTextStyles.label,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                const SizedBox(width: 8),
                Text(
                  formatRupees(fare),
                  style: AppTextStyles.h3.copyWith(
                    color: AppColors.statusSuccess,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 6),
            Row(
              children: [
                Text(
                  VehicleType.label(ride.vehicleType),
                  style: AppTextStyles.bodySmall,
                ),
                const SizedBox(width: 8),
                const Icon(
                  Icons.fiber_manual_record,
                  size: 4,
                  color: AppColors.textTertiary,
                ),
                const SizedBox(width: 8),
                Text(
                  _fmtTs(ride.completedAt ?? ride.requestedAt),
                  style: AppTextStyles.bodySmall,
                ),
                const Spacer(),
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 2,
                  ),
                  decoration: BoxDecoration(
                    color: AppColors.statusSuccess.withValues(alpha: 0.16),
                    borderRadius: BorderRadius.circular(99),
                  ),
                  child: Text(
                    'Commission ₹0',
                    style: AppTextStyles.labelSmall.copyWith(
                      color: AppColors.statusSuccess,
                    ),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
