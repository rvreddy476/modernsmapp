import 'package:atpost_app/core/utils/app_logger.dart';

/// Sprint 5 — Mopedu crash-reporting breadcrumbs.
///
/// We do not yet bundle `sentry_flutter` (pubspec is frozen in this sprint).
/// This file is the no-op interface that screens call into. When the app
/// adopts Sentry / Crashlytics later, swap the body of `add()`, `error()`,
/// and `setUser()` to call into the real SDK; everything else stays.
///
/// Design constraints:
///   - Zero allocations in the hot path beyond the log call so we can
///     liberally instrument every screen.
///   - Body of breadcrumbs is *never* PII. Drop emails, phone numbers,
///     OTPs, lat/lng, addresses, document numbers, full ride payloads.
///   - Categories are namespaced: `mopedu.<surface>.<event>` (e.g.
///     `mopedu.home.opened`, `mopedu.partner.online_toggled`). Don't
///     invent ad-hoc names — use the constants in [MopeduBreadcrumbs] or
///     add new ones here.
class MopeduBreadcrumbs {
  MopeduBreadcrumbs._();

  /// Add an info breadcrumb. Forwards to AppLogger today; will forward to
  /// Sentry SDK when wired.
  static void add(
    String category, {
    String? message,
    Map<String, dynamic>? data,
  }) {
    final dataStr = (data == null || data.isEmpty) ? '' : ' $data';
    AppLogger.info(
      '[breadcrumb] $category${message != null ? ' — $message' : ''}$dataStr',
      tag: 'mopedu',
    );
  }

  /// Note an exception/error breadcrumb. Mirrors `add()` but also threads
  /// the error + stacktrace through AppLogger.error which has special
  /// production handling.
  static void error(
    String message, {
    Object? error,
    StackTrace? stackTrace,
    Map<String, dynamic>? data,
  }) {
    final dataStr = (data == null || data.isEmpty) ? '' : ' $data';
    AppLogger.error(
      '[breadcrumb] $message$dataStr',
      tag: 'mopedu',
      error: error,
      stackTrace: stackTrace,
    );
  }

  /// Sets the active user-id context for crash reports. Passing `null`
  /// clears it (used on logout). The user-id is the ONLY user identifier
  /// we attach — names, emails, phone numbers must never come through.
  static void setUser(String? userId) {
    AppLogger.info(
      '[breadcrumb] user.context userId=${userId ?? '(cleared)'}',
      tag: 'mopedu',
    );
  }

  // ------------------------------------------------------------------
  // Helper hooks per Mopedu surface. Screens import these by name so the
  // breadcrumb categories stay consistent and a future grep is sane.
  // ------------------------------------------------------------------

  /// Generic screen-open breadcrumb. Call from `initState`.
  static void screen(String name, {Map<String, dynamic>? data}) {
    add('mopedu.screen.$name', data: data);
  }

  // ─── Customer home + booking ────────────────────────────────────────

  static void homeOpened() => add('mopedu.home.opened');
  static void homeBookTapped({String? vehicleType, String? cityId}) => add(
        'mopedu.home.book_tapped',
        data: {
          if (vehicleType != null) 'vehicle_type': vehicleType,
          if (cityId != null) 'city_id': cityId,
        },
      );

  /// Booking-in-progress: state machine transitions. Phase is one of
  /// `searching`, `accepted`, `arriving`, `arrived`, `in_progress`,
  /// `completed`, `cancelled`, `expired`. Ride-id is included so the
  /// correlation works in Sentry; it is not PII.
  static void bookingState({
    required String rideId,
    required String phase,
  }) =>
      add(
        'mopedu.booking.state',
        data: {'ride_id': rideId, 'phase': phase},
      );

  // ─── Partner ────────────────────────────────────────────────────────

  static void partnerOnlineToggled({required bool online}) => add(
        'mopedu.partner.online_toggled',
        data: {'online': online},
      );

  /// Ride lifecycle from the partner side: `accept`, `arriving`,
  /// `arrived`, `start`, `complete`, `cancel`, `reject`. We never log OTP.
  static void partnerRideAction({
    required String rideId,
    required String action,
  }) =>
      add(
        'mopedu.partner.ride_action',
        data: {'ride_id': rideId, 'action': action},
      );

  // ─── Subscription ──────────────────────────────────────────────────

  static void subscriptionRenewStart({required String planId}) => add(
        'mopedu.subscription.renew_start',
        data: {'plan_id': planId},
      );
  static void subscriptionRenewComplete({required String planId}) => add(
        'mopedu.subscription.renew_complete',
        data: {'plan_id': planId},
      );
  static void subscriptionRenewFail({
    required String planId,
    String? reason,
  }) =>
      add(
        'mopedu.subscription.renew_fail',
        data: {
          'plan_id': planId,
          if (reason != null) 'reason': reason,
        },
      );

  // ─── Safety ────────────────────────────────────────────────────────

  static void safetyCenterOpen() => add('mopedu.safety.center_open');
  static void sosTriggered({String? rideId}) => add(
        'mopedu.safety.sos_triggered',
        data: {if (rideId != null) 'ride_id': rideId},
      );
  static void sosConfirmed({String? rideId}) => add(
        'mopedu.safety.sos_confirmed',
        data: {if (rideId != null) 'ride_id': rideId},
      );

  // ─── Gate ──────────────────────────────────────────────────────────

  static void gateBlocked({required String reason, String? city}) => add(
        'mopedu.gate.blocked',
        data: {
          'reason': reason,
          if (city != null) 'city': city,
        },
      );
  static void waitlistJoined({String? city}) => add(
        'mopedu.waitlist.joined',
        data: {if (city != null) 'city': city},
      );
}
