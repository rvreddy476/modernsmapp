// Mopedu home — Sprint 1 (customer side).
//
// Surface:
//   - AppBar with the current city chip (tap to change) + a safety shield.
//   - Hero card: pickup + drop pin selectors. v1 uses text inputs because
//     `google_maps_flutter` isn't in pubspec; production map UI is Sprint 2.
//   - Vehicle type carousel (bike → premium) with per-vehicle fare estimate.
//   - Saved-places section (chips for Home / Work / School / Hospital +
//     recents). Tap to set drop. Long-press to edit.
//   - Estimated fare pill at the bottom.
//   - "Book ride" sticky CTA (disabled until pickup + drop + vehicle +
//     fare estimate are ready).
//   - Pull-to-refresh.
//
// Telemetry: `mopedu.opened` fires once after first frame. Estimate +
// city + saved-place events fire from the providers/notifier layer.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/features/mopedu/city_picker_sheet.dart';
import 'package:atpost_app/features/mopedu/map_picker_screen.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:atpost_app/services/mopedu_crash_breadcrumbs.dart';
import 'package:atpost_app/services/mopedu_location_service.dart';
import 'package:atpost_app/services/mopedu_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class MopeduHomeScreen extends ConsumerStatefulWidget {
  const MopeduHomeScreen({super.key});

  @override
  ConsumerState<MopeduHomeScreen> createState() => _MopeduHomeScreenState();
}

class _MopeduHomeScreenState extends ConsumerState<MopeduHomeScreen> {
  bool _firedOpened = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_firedOpened) return;
      _firedOpened = true;
      ref.read(mopeduTelemetryProvider).mopeduOpened();
      MopeduBreadcrumbs.homeOpened();
      _maybeAutoSelectCity();
    });
  }

  /// If the user hasn't picked a city yet, default to the first active
  /// city returned by the backend (typically Bengaluru).
  Future<void> _maybeAutoSelectCity() async {
    final selected = ref.read(selectedCityProvider);
    if (selected != null) return;
    final cities = await ref.read(riderCitiesProvider.future);
    final active = cities.where((c) => c.isActive).toList();
    if (active.isEmpty) return;
    await ref.read(selectedCityProvider.notifier).select(active.first.id);
    final notifier = ref.read(mopeduBookingNotifier.notifier);
    notifier.setCityId(active.first.id);
  }

  Future<void> _refresh() async {
    ref.invalidate(riderCitiesProvider);
    ref.invalidate(savedPlacesProvider);
    final st = ref.read(mopeduBookingNotifier);
    if (st.canEstimate) {
      ref.invalidate(
        fareEstimateProvider(
          FareQuery(
            pickup: st.pickup!,
            drop: st.drop!,
            vehicleType: st.vehicleType ?? VehicleType.auto,
            cityId: st.cityId!,
          ),
        ),
      );
    }
    await ref.read(riderCitiesProvider.future);
  }

  @override
  Widget build(BuildContext context) {
    final booking = ref.watch(mopeduBookingNotifier);
    final cities = ref.watch(riderCitiesProvider);
    final selectedCityId = ref.watch(selectedCityProvider);
    final cityName = cities.maybeWhen(
      data: (list) {
        if (selectedCityId == null) return 'Choose city';
        final m = list.where((c) => c.id == selectedCityId).toList();
        return m.isEmpty ? 'Choose city' : m.first.name;
      },
      orElse: () => 'Choose city',
    );

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Row(
          children: [
            Text('Mopedu', style: AppTextStyles.logo),
            const SizedBox(width: 10),
            _CityChip(name: cityName, onTap: _onPickCity),
          ],
        ),
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
          IconButton(
            tooltip: 'My rides',
            icon: const Icon(Icons.history),
            onPressed: () => context.push('/mopedu/rides'),
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: _refresh,
        child: ListView(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 120),
          children: [
            const _PickupDropCard(),
            const SizedBox(height: 16),
            const _VehicleCarousel(),
            const SizedBox(height: 16),
            const _SavedPlacesSection(),
            const SizedBox(height: 16),
            _FarePill(booking: booking),
          ],
        ),
      ),
      bottomSheet: const _BookCta(),
    );
  }

  Future<void> _onPickCity() async {
    final id = await CityPickerSheet.show(context);
    if (id != null) {
      ref.read(mopeduBookingNotifier.notifier).setCityId(id);
    }
  }
}

// ─── App-bar city chip ────────────────────────────────────────────────

class _CityChip extends StatelessWidget {
  const _CityChip({required this.name, required this.onTap});

  final String name;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        borderRadius: BorderRadius.circular(99),
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.circular(99),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(
                Icons.location_city,
                color: AppColors.textTertiary,
                size: 14,
              ),
              const SizedBox(width: 4),
              Text(name, style: AppTextStyles.labelSmall),
              const SizedBox(width: 2),
              const Icon(
                Icons.expand_more,
                color: AppColors.textTertiary,
                size: 14,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

// ─── Pickup + drop card ───────────────────────────────────────────────

class _PickupDropCard extends ConsumerWidget {
  const _PickupDropCard();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final booking = ref.watch(mopeduBookingNotifier);
    final notifier = ref.read(mopeduBookingNotifier.notifier);

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
              Text('Where to?', style: AppTextStyles.h3),
              const Spacer(),
              TextButton.icon(
                onPressed: () => _useCurrentLocation(context, notifier),
                icon: const Icon(Icons.my_location, size: 14),
                label: Text(
                  'Use current location',
                  style: AppTextStyles.labelSmall,
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          _PointRow(
            icon: Icons.trip_origin,
            iconColor: AppColors.postbookPrimary,
            label: 'Pickup',
            point: booking.pickup,
            onTap: () => _pickOnMap(
              context,
              mode: MapPickerMode.pickup,
              current: booking.pickup,
              onSave: notifier.setPickup,
            ),
            onClear: booking.pickup == null
                ? null
                : () => notifier.setPickup(null),
          ),
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 4),
            child: Divider(color: AppColors.borderSubtle, height: 1),
          ),
          _PointRow(
            icon: Icons.location_on,
            iconColor: AppColors.statusError,
            label: 'Drop',
            point: booking.drop,
            onTap: () => _pickOnMap(
              context,
              mode: MapPickerMode.drop,
              current: booking.drop,
              onSave: notifier.setDrop,
            ),
            onClear: booking.drop == null
                ? null
                : () => notifier.setDrop(null),
          ),
        ],
      ),
    );
  }

  Future<void> _useCurrentLocation(
    BuildContext context,
    MopeduBookingNotifier notifier,
  ) async {
    final messenger = ScaffoldMessenger.of(context);
    messenger.showSnackBar(
      const SnackBar(
        content: Text('Locating you…'),
        duration: Duration(seconds: 2),
      ),
    );
    final result = await MopeduLocationService.getCurrentPosition();
    if (!context.mounted) return;
    if (!result.isOk) {
      messenger.showSnackBar(
        SnackBar(content: Text(MopeduLocationService.describe(result.failure!))),
      );
      return;
    }
    final pos = result.position!;
    notifier.setPickup(
      RidePoint(
        lat: pos.latitude,
        lng: pos.longitude,
        placeName: 'Current location',
      ),
    );
  }

  /// Opens the Google Maps picker (C6). Falls through to the
  /// manual-coordinate sheet only when the caller explicitly wants
  /// the textual fallback (e.g. on a device without Maps).
  Future<void> _pickOnMap(
    BuildContext context, {
    required MapPickerMode mode,
    required RidePoint? current,
    required void Function(RidePoint?) onSave,
  }) async {
    final result = await MopeduMapPicker.show(
      context,
      mode: mode,
      initial: current,
    );
    if (result != null) onSave(result);
  }

}

class _PointRow extends StatelessWidget {
  const _PointRow({
    required this.icon,
    required this.iconColor,
    required this.label,
    required this.point,
    required this.onTap,
    this.onClear,
  });

  final IconData icon;
  final Color iconColor;
  final String label;
  final RidePoint? point;
  final VoidCallback onTap;
  final VoidCallback? onClear;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 10, horizontal: 4),
        child: Row(
          children: [
            Icon(icon, color: iconColor, size: 18),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(label, style: AppTextStyles.labelSmall),
                  const SizedBox(height: 2),
                  Text(
                    point?.displayName ?? 'Tap to set $label',
                    style: AppTextStyles.label.copyWith(
                      color: point == null
                          ? AppColors.textMuted
                          : AppColors.textPrimary,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ),
            ),
            if (onClear != null)
              IconButton(
                icon: const Icon(
                  Icons.close,
                  color: AppColors.textTertiary,
                  size: 16,
                ),
                onPressed: onClear,
              ),
          ],
        ),
      ),
    );
  }
}

// ─── Vehicle carousel ─────────────────────────────────────────────────

class _VehicleCarousel extends ConsumerWidget {
  const _VehicleCarousel();

  static const _icons = <String, IconData>{
    VehicleType.bike: Icons.two_wheeler,
    VehicleType.auto: Icons.electric_rickshaw,
    VehicleType.miniCab: Icons.directions_car,
    VehicleType.sedan: Icons.directions_car_filled,
    VehicleType.suv: Icons.airport_shuttle,
    VehicleType.premium: Icons.local_taxi,
  };

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final booking = ref.watch(mopeduBookingNotifier);
    final notifier = ref.read(mopeduBookingNotifier.notifier);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(left: 4, bottom: 8),
          child: Text('Vehicle', style: AppTextStyles.h3),
        ),
        SizedBox(
          height: 120,
          child: ListView.separated(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(horizontal: 4),
            itemCount: VehicleType.all.length,
            separatorBuilder: (_, _) => const SizedBox(width: 10),
            itemBuilder: (context, i) {
              final type = VehicleType.all[i];
              return _VehicleCard(
                type: type,
                icon: _icons[type] ?? Icons.directions_car,
                isSelected: booking.vehicleType == type,
                onTap: () => notifier.setVehicleType(type),
                booking: booking,
              );
            },
          ),
        ),
      ],
    );
  }
}

class _VehicleCard extends ConsumerWidget {
  const _VehicleCard({
    required this.type,
    required this.icon,
    required this.isSelected,
    required this.onTap,
    required this.booking,
  });

  final String type;
  final IconData icon;
  final bool isSelected;
  final VoidCallback onTap;
  final MopeduBookingState booking;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    String fareLabel = '—';
    int? eta;
    if (booking.canEstimate) {
      final q = FareQuery(
        pickup: booking.pickup!,
        drop: booking.drop!,
        vehicleType: type,
        cityId: booking.cityId!,
      );
      final est = ref.watch(fareEstimateProvider(q));
      est.when(
        data: (e) {
          fareLabel = formatRupees(e.fareEstimatePaise);
          eta = e.etaToPickupSeconds;
          // Push the chosen vehicle's estimate into the booking state so
          // the Book CTA enables.
          if (isSelected && booking.estimate != e) {
            WidgetsBinding.instance.addPostFrameCallback((_) {
              ref.read(mopeduBookingNotifier.notifier).setEstimate(e);
            });
          }
        },
        loading: () {
          fareLabel = '...';
        },
        error: (_, _) {
          fareLabel = '—';
        },
      );
    }

    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      onTap: onTap,
      child: Container(
        width: 120,
        padding: const EdgeInsets.all(10),
        decoration: BoxDecoration(
          color: isSelected
              ? AppColors.postbookPrimary.withValues(alpha: 0.12)
              : AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(
            color: isSelected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
            width: isSelected ? 1.5 : 1,
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(
              icon,
              color: isSelected
                  ? AppColors.postbookPrimary
                  : AppColors.textTertiary,
              size: 28,
            ),
            const SizedBox(height: 8),
            Text(VehicleType.label(type), style: AppTextStyles.label),
            const SizedBox(height: 2),
            Text(fareLabel, style: AppTextStyles.bodySmall),
            if (eta != null && eta! > 0)
              Text(
                '${(eta! / 60).ceil()} min away',
                style: AppTextStyles.labelSmall,
              ),
          ],
        ),
      ),
    );
  }
}

// ─── Saved places ─────────────────────────────────────────────────────

class _SavedPlacesSection extends ConsumerWidget {
  const _SavedPlacesSection();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncPlaces = ref.watch(savedPlacesProvider);
    final places = asyncPlaces.maybeWhen(
      data: (l) => l,
      orElse: () => const <SavedPlace>[],
    );

    final byKind = <String, SavedPlace?>{
      SavedPlaceKind.home: null,
      SavedPlaceKind.work: null,
      SavedPlaceKind.school: null,
      SavedPlaceKind.hospital: null,
    };
    for (final p in places) {
      if (byKind.containsKey(p.kind)) byKind[p.kind] = p;
    }
    final recents = places
        .where((p) => p.kind == SavedPlaceKind.recent)
        .toList()
        .reversed
        .toList();

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(left: 4, bottom: 8),
          child: Row(
            children: [
              Text('Saved places', style: AppTextStyles.h3),
              const Spacer(),
              TextButton(
                onPressed: () => context.push('/mopedu/saved-places'),
                child: Text('Manage', style: AppTextStyles.labelSmall),
              ),
            ],
          ),
        ),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            for (final kind in SavedPlaceKind.fixed)
              _SavedChip(
                kind: kind,
                place: byKind[kind],
              ),
            for (final r in recents.take(5))
              _SavedChip(kind: SavedPlaceKind.recent, place: r),
          ],
        ),
      ],
    );
  }
}

class _SavedChip extends ConsumerWidget {
  const _SavedChip({required this.kind, required this.place});

  final String kind;
  final SavedPlace? place;

  IconData get _icon {
    switch (kind) {
      case SavedPlaceKind.home:
        return Icons.home;
      case SavedPlaceKind.work:
        return Icons.work;
      case SavedPlaceKind.school:
        return Icons.school;
      case SavedPlaceKind.hospital:
        return Icons.local_hospital;
      default:
        return Icons.place;
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final has = place != null;
    final label = has ? place!.label : SavedPlaceKind.label(kind);
    return InkWell(
      borderRadius: BorderRadius.circular(99),
      onTap: () {
        if (has) {
          ref.read(mopeduBookingNotifier.notifier).setDrop(place!.point);
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text('Drop set to ${place!.label}')),
          );
        } else {
          context.push('/mopedu/saved-places');
        }
      },
      onLongPress: () => context.push('/mopedu/saved-places'),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(99),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              _icon,
              size: 14,
              color: has
                  ? AppColors.postbookPrimary
                  : AppColors.textTertiary,
            ),
            const SizedBox(width: 6),
            Text(label, style: AppTextStyles.labelSmall),
            if (!has) ...[
              const SizedBox(width: 4),
              const Icon(
                Icons.add,
                size: 12,
                color: AppColors.textTertiary,
              ),
            ],
          ],
        ),
      ),
    );
  }
}

// ─── Fare pill ────────────────────────────────────────────────────────

class _FarePill extends StatelessWidget {
  const _FarePill({required this.booking});

  final MopeduBookingState booking;

  @override
  Widget build(BuildContext context) {
    if (booking.estimate == null) return const SizedBox.shrink();
    final est = booking.estimate!;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          const Icon(
            Icons.receipt_long,
            color: AppColors.postbookPrimary,
            size: 20,
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Estimated fare', style: AppTextStyles.labelSmall),
                Text(
                  formatRupees(est.fareEstimatePaise),
                  style: AppTextStyles.h2,
                ),
              ],
            ),
          ),
          Column(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              Text(
                '${est.estimatedDistanceKm.toStringAsFixed(1)} km · '
                '${est.estimatedDurationMin} min',
                style: AppTextStyles.bodySmall,
              ),
              if (est.surgeMultiplier > 1.0)
                Text(
                  'Surge ${est.surgeMultiplier.toStringAsFixed(1)}x',
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.statusWarning,
                  ),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

// ─── Sticky Book CTA ──────────────────────────────────────────────────

class _BookCta extends ConsumerWidget {
  const _BookCta();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final booking = ref.watch(mopeduBookingNotifier);
    final canBook =
        booking.canBook && booking.phase != MopeduBookingPhase.booking;

    return SafeArea(
      child: Container(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
        decoration: const BoxDecoration(
          color: AppColors.bgPrimary,
          border: Border(
            top: BorderSide(color: AppColors.borderSubtle, width: 0.5),
          ),
        ),
        child: SizedBox(
          width: double.infinity,
          height: 50,
          child: ElevatedButton(
            style: ElevatedButton.styleFrom(
              backgroundColor: canBook
                  ? AppColors.postbookPrimary
                  : AppColors.bgTertiary,
              foregroundColor: Colors.white,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
              ),
            ),
            onPressed: canBook ? () => _book(context, ref) : null,
            child: booking.phase == MopeduBookingPhase.booking
                ? const SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(
                      color: Colors.white,
                      strokeWidth: 2,
                    ),
                  )
                : Text(
                    canBook ? 'Book ride' : 'Set pickup, drop & vehicle',
                    style: AppTextStyles.h3.copyWith(color: Colors.white),
                  ),
          ),
        ),
      ),
    );
  }

  Future<void> _book(BuildContext context, WidgetRef ref) async {
    final notifier = ref.read(mopeduBookingNotifier.notifier);
    final st = ref.read(mopeduBookingNotifier);
    MopeduBreadcrumbs.homeBookTapped(
      vehicleType: st.vehicleType,
      cityId: st.cityId,
    );
    final id = await notifier.book();
    if (id != null && context.mounted) {
      context.push('/mopedu/booking/$id');
    } else if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not book ride. Please retry.')),
      );
    }
  }
}
