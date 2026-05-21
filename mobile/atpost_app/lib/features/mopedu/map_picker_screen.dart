// Mopedu map picker — Wave C6.
//
// Replaces the manual lat/lng text inputs in the booking flow with a
// Google-Maps-backed tap-to-pick experience. Two modes:
//
//   - pickup mode  → drops a green origin pin on tap.
//   - drop mode    → drops a red destination pin on tap.
//
// The user can drag the pin to refine the position; the "Confirm" CTA
// returns the final [RidePoint] back to the caller via Navigator.pop.
//
// Production setup required (NOT shipped — Maps requires per-platform
// API keys that can't live in source control):
//
//   1. Android: add to AndroidManifest.xml inside <application>:
//        <meta-data
//          android:name="com.google.android.geo.API_KEY"
//          android:value="${MAPS_API_KEY}"/>
//      Provide MAPS_API_KEY via gradle.properties (gitignored) or
//      via a build flavor.
//
//   2. iOS: in ios/Runner/AppDelegate.swift:
//        import GoogleMaps
//        GMSServices.provideAPIKey(<MAPS_API_KEY>)
//      Then `pod install` after running `flutter pub get`.
//
//   3. Web: in web/index.html <head>:
//        <script
//          src="https://maps.googleapis.com/maps/api/js?key=${MAPS_API_KEY}"
//          async defer></script>
//
// If google_maps_flutter cannot find a key, GoogleMap renders a gray
// rectangle — the user can still tap, and the lat/lng selection
// behavior still works (we never lookup tiles ourselves).

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/services/mopedu_location_service.dart';
import 'package:flutter/material.dart';
import 'package:google_maps_flutter/google_maps_flutter.dart';

enum MapPickerMode { pickup, drop }

class MopeduMapPicker extends StatefulWidget {
  const MopeduMapPicker({
    super.key,
    required this.mode,
    this.initial,
    this.title,
  });

  final MapPickerMode mode;
  final RidePoint? initial;
  final String? title;

  /// Convenience: opens the picker as a full-screen route.
  /// Returns null if the user backs out.
  static Future<RidePoint?> show(
    BuildContext context, {
    required MapPickerMode mode,
    RidePoint? initial,
    String? title,
  }) {
    return Navigator.of(context).push<RidePoint>(MaterialPageRoute(
      builder: (_) =>
          MopeduMapPicker(mode: mode, initial: initial, title: title),
      fullscreenDialog: true,
    ));
  }

  @override
  State<MopeduMapPicker> createState() => _MopeduMapPickerState();
}

class _MopeduMapPickerState extends State<MopeduMapPicker> {
  GoogleMapController? _controller;
  LatLng? _picked;
  bool _locating = false;

  static const _bengaluruCentroid = LatLng(12.9716, 77.5946);

  @override
  void initState() {
    super.initState();
    final init = widget.initial;
    if (init != null) {
      _picked = LatLng(init.lat, init.lng);
    } else {
      // Best-effort: pre-fill the picked point with the device's
      // current location so the camera lands somewhere useful.
      _seedFromDeviceLocation();
    }
  }

  Future<void> _seedFromDeviceLocation() async {
    setState(() => _locating = true);
    final result = await MopeduLocationService.getCurrentPosition();
    if (!mounted) return;
    setState(() {
      _locating = false;
      if (result.isOk) {
        _picked = LatLng(result.position!.latitude, result.position!.longitude);
      }
    });
    // Recenter the camera once the controller is wired.
    final c = _controller;
    final p = _picked;
    if (c != null && p != null) {
      c.animateCamera(CameraUpdate.newLatLng(p));
    }
  }

  Color get _pinColor => widget.mode == MapPickerMode.pickup
      ? AppColors.postbookPrimary
      : AppColors.statusError;

  @override
  Widget build(BuildContext context) {
    final initialCamera = CameraPosition(
      target: _picked ?? _bengaluruCentroid,
      zoom: _picked != null ? 16 : 12,
    );

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        title: Text(
          widget.title ??
              (widget.mode == MapPickerMode.pickup ? 'Set pickup' : 'Set drop'),
        ),
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
      ),
      body: Stack(
        children: [
          GoogleMap(
            initialCameraPosition: initialCamera,
            onMapCreated: (c) => _controller = c,
            onTap: (latlng) => setState(() => _picked = latlng),
            markers: _picked == null
                ? const <Marker>{}
                : {
                    Marker(
                      markerId: MarkerId(widget.mode.name),
                      position: _picked!,
                      draggable: true,
                      onDragEnd: (newPos) => setState(() => _picked = newPos),
                      icon: BitmapDescriptor.defaultMarkerWithHue(
                        widget.mode == MapPickerMode.pickup
                            ? BitmapDescriptor.hueGreen
                            : BitmapDescriptor.hueRed,
                      ),
                    ),
                  },
            myLocationEnabled: true,
            myLocationButtonEnabled: true,
            zoomControlsEnabled: false,
          ),
          if (_locating)
            const Positioned(
              top: 16,
              left: 16,
              child: _LocatingChip(),
            ),
        ],
      ),
      bottomSheet: SafeArea(
        child: Container(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
          decoration: const BoxDecoration(
            color: AppColors.bgPrimary,
            border: Border(
              top: BorderSide(color: AppColors.borderSubtle, width: 0.5),
            ),
          ),
          child: Row(
            children: [
              Icon(Icons.location_on, color: _pinColor, size: 18),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  _picked == null
                      ? 'Tap the map to drop a pin'
                      : '${_picked!.latitude.toStringAsFixed(5)}, '
                          '${_picked!.longitude.toStringAsFixed(5)}',
                  style: AppTextStyles.bodySmall,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              const SizedBox(width: 8),
              ElevatedButton(
                style: ElevatedButton.styleFrom(
                  backgroundColor: _picked == null
                      ? AppColors.bgTertiary
                      : AppColors.postbookPrimary,
                  foregroundColor: Colors.white,
                  shape: RoundedRectangleBorder(
                    borderRadius:
                        BorderRadius.circular(AppSpacing.radiusMedium),
                  ),
                ),
                onPressed: _picked == null
                    ? null
                    : () {
                        Navigator.of(context).pop(RidePoint(
                          lat: _picked!.latitude,
                          lng: _picked!.longitude,
                          placeName: widget.mode == MapPickerMode.pickup
                              ? 'Pickup pin'
                              : 'Drop pin',
                        ));
                      },
                child: const Text('Confirm'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _LocatingChip extends StatelessWidget {
  const _LocatingChip();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(99),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          const SizedBox(
            width: 12,
            height: 12,
            child: CircularProgressIndicator(strokeWidth: 2),
          ),
          const SizedBox(width: 8),
          Text('Locating…', style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}
