// Mopedu device-location helper.
//
// Single source of truth for getting / streaming the device's GPS
// position. Wraps `geolocator` so screens + providers don't take a
// direct dependency on the plugin and we have one place to handle
// permission denials.
//
// PRIVACY: lat/lng never go through telemetry. Callers may log
// `LocationFailure.reason` (categorical), never coordinates.

import 'dart:async';

import 'package:geolocator/geolocator.dart';

enum LocationFailure {
  servicesDisabled,
  permissionDenied,
  permissionDeniedForever,
  timeout,
  unknown,
}

class LocationResult {
  const LocationResult({this.position, this.failure});

  /// Latest device position. Null when [failure] is set.
  final Position? position;

  /// Non-null when the lookup did not yield a fix.
  final LocationFailure? failure;

  bool get isOk => position != null && failure == null;
}

class MopeduLocationService {
  /// One-shot best-effort GPS read.
  ///
  /// Walks the permission ladder. On a hard denial returns a
  /// `LocationResult` with the appropriate [LocationFailure] so the UI
  /// can render a guidance row instead of silently doing nothing.
  static Future<LocationResult> getCurrentPosition({
    Duration timeout = const Duration(seconds: 8),
  }) async {
    try {
      final servicesEnabled = await Geolocator.isLocationServiceEnabled();
      if (!servicesEnabled) {
        return const LocationResult(failure: LocationFailure.servicesDisabled);
      }
      var permission = await Geolocator.checkPermission();
      if (permission == LocationPermission.denied) {
        permission = await Geolocator.requestPermission();
      }
      if (permission == LocationPermission.deniedForever) {
        return const LocationResult(
          failure: LocationFailure.permissionDeniedForever,
        );
      }
      if (permission == LocationPermission.denied) {
        return const LocationResult(failure: LocationFailure.permissionDenied);
      }
      final pos = await Geolocator.getCurrentPosition(
        desiredAccuracy: LocationAccuracy.high,
        timeLimit: timeout,
      );
      return LocationResult(position: pos);
    } on TimeoutException {
      return const LocationResult(failure: LocationFailure.timeout);
    } catch (_) {
      return const LocationResult(failure: LocationFailure.unknown);
    }
  }

  /// Streams positions filtered by [distanceFilter] metres. Caller
  /// owns the subscription and must cancel it.
  static Stream<Position> positionStream({int distanceFilter = 25}) {
    return Geolocator.getPositionStream(
      locationSettings: LocationSettings(
        accuracy: LocationAccuracy.high,
        distanceFilter: distanceFilter,
      ),
    );
  }
  // ^ getPositionStream does accept locationSettings in 12.x; only the
  // one-shot getCurrentPosition uses desiredAccuracy + timeLimit.

  /// Human-readable label for [failure]. Stable strings — used in
  /// SnackBars and breadcrumbs.
  static String describe(LocationFailure failure) {
    switch (failure) {
      case LocationFailure.servicesDisabled:
        return 'Turn on device location to use this.';
      case LocationFailure.permissionDenied:
        return 'Location permission was denied.';
      case LocationFailure.permissionDeniedForever:
        return 'Location permission is blocked. Enable it in settings.';
      case LocationFailure.timeout:
        return 'Could not get a location fix. Try again outside.';
      case LocationFailure.unknown:
        return 'Could not read your location.';
    }
  }
}
