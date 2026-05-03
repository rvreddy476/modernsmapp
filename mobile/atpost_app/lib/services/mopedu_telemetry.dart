// Mopedu telemetry — Sprint 1 (customer side).
//
// Mirrors the contract used by `billpay_telemetry.dart` and
// `shell_telemetry.dart`: in-memory queue, 30s flush, `/v1/analytics/events`,
// banned-prop sanitisation.
//
// PRIVACY (spec §13, DPDP):
//   * NEVER log lat / lng / latitude / longitude / address / place_name.
//   * NEVER log partner_id, ride_id, customer_id, phone, otp.
//   * Fares are bucketed via `amountBucket(paise)` — never raw amounts.
//   * `vehicle_type` and `city_id` are categorical and ARE allowed.
//   * `stage` (cancellation step) is categorical and allowed.
//
// The sanitiser drops banned keys at emit time even if a caller forgets
// the rules — defence in depth.

import 'dart:async';
import 'dart:collection';

import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

const Set<String> _bannedPropKeys = {
  'lat',
  'lng',
  'latitude',
  'longitude',
  'pickup_lat',
  'pickup_lng',
  'drop_lat',
  'drop_lng',
  'address',
  'pickup_address',
  'drop_address',
  'place_name',
  'pickup_place_name',
  'drop_place_name',
  'partner_id',
  'partner_name',
  'partner_phone',
  'driver_id',
  'driver_name',
  'ride_id',
  'customer_id',
  'user_id',
  'phone',
  'email',
  'otp',
  'vehicle_number',
  'amount_paise',
  'amount_inr',
  'amount',
  'rupees',
  'fare_paise',
  'final_fare_paise',
  'idempotency_key',
  // ── Sprint 4 additions: never log raw earnings, doc numbers, or the
  // referral code itself (the code is derived from partner_id).
  'earnings_paise',
  'today_earnings_paise',
  'total_earnings_paise',
  'avg_fare_paise',
  'document_number',
  'aadhaar_number',
  'pan_number',
  'license_number',
  'referral_code',
};

class MopeduTelemetryEvents {
  MopeduTelemetryEvents._();

  static const opened = 'mopedu.opened';
  static const estimateRequested = 'mopedu.estimate.requested';
  static const rideCreated = 'mopedu.ride.created';
  static const rideCancelled = 'mopedu.ride.cancelled';
  static const rideCompleted = 'mopedu.ride.completed';
  static const savedPlaceAdded = 'mopedu.saved_place.added';
  static const cityChanged = 'mopedu.city.changed';

  // ── Sprint 2 — partner side ────────────────────────────────────────
  static const partnerOnboardingStep = 'mopedu.partner.onboarding.step';
  static const partnerSubscribed = 'mopedu.partner.subscribed';
  static const partnerOnlineToggled = 'mopedu.partner.online.toggled';
  static const partnerOfferReceived = 'mopedu.partner.offer.received';
  static const partnerOfferAccepted = 'mopedu.partner.offer.accepted';
  static const partnerOfferRejected = 'mopedu.partner.offer.rejected';
  static const partnerRideCompleted = 'mopedu.partner.ride.completed';
  static const partnerSubscriptionRenewed =
      'mopedu.partner.subscription.renewed';

  // ── Sprint 3 — customer-side safety ────────────────────────────────
  static const safetyOpened = 'mopedu.safety.opened';
  static const safetySosTriggered = 'mopedu.safety.sos_triggered';
  static const safetyTrustedContactSet =
      'mopedu.safety.trusted_contact_set';
  static const safetyShareRideCreated =
      'mopedu.safety.share_ride_created';
  static const complaintSubmitted = 'mopedu.complaint.submitted';

  // ── Sprint 4 — partner polish ──────────────────────────────────────
  static const partnerSubscriptionRenewedViaWallet =
      'mopedu.partner.subscription.renewed_via_wallet';
  static const partnerSubscriptionRenewedViaUpi =
      'mopedu.partner.subscription.renewed_via_upi';
  static const partnerAutoRenewToggled =
      'mopedu.partner.subscription.auto_renew_toggled';
  static const partnerSubscriptionSwitched =
      'mopedu.partner.subscription.switched';
  static const partnerInvoiceRequested = 'mopedu.partner.invoice.requested';
  static const partnerReferralShared = 'mopedu.partner.referral.shared';
  static const partnerDocsExpiringBannerShown =
      'mopedu.partner.docs.expiring_banner_shown';
}

/// Stages at which a customer can cancel — used by `rideCancelled`.
class MopeduCancelStage {
  MopeduCancelStage._();

  static const searching = 'searching';
  static const assigned = 'assigned';
  static const arriving = 'arriving';
}

class _QueuedEvent {
  _QueuedEvent({
    required this.name,
    required this.timestamp,
    required this.props,
  });

  final String name;
  final DateTime timestamp;
  final Map<String, Object?> props;

  Map<String, dynamic> toJson() => {
        'name': name,
        'ts': timestamp.toUtc().toIso8601String(),
        'props': props,
      };
}

class MopeduTelemetry {
  MopeduTelemetry(this._client);

  final ApiClient _client;
  final Queue<_QueuedEvent> _buffer = Queue<_QueuedEvent>();
  Timer? _timer;
  bool _flushing = false;

  static const int _maxBuffered = 500;
  static const Duration _flushInterval = Duration(seconds: 30);

  void start() {
    _timer ??= Timer.periodic(_flushInterval, (_) => _flush());
  }

  void stop() {
    _timer?.cancel();
    _timer = null;
  }

  void emit(String name, {Map<String, Object?>? props}) {
    final cleaned = _sanitize(props ?? const {});
    if (_buffer.length >= _maxBuffered) {
      _buffer.removeFirst();
    }
    _buffer.add(
      _QueuedEvent(
        name: name,
        timestamp: DateTime.now(),
        props: cleaned,
      ),
    );
    start();
  }

  // ── Convenience wrappers ─────────────────────────────────────────────

  void mopeduOpened() => emit(MopeduTelemetryEvents.opened);

  void mopeduEstimateRequested({
    required String vehicleType,
    required String cityId,
  }) {
    emit(
      MopeduTelemetryEvents.estimateRequested,
      props: {
        'vehicle_type': vehicleType,
        'city_id': cityId,
      },
    );
  }

  void mopeduRideCreated({
    required String vehicleType,
    required String cityId,
    required int fareEstimatePaise,
  }) {
    emit(
      MopeduTelemetryEvents.rideCreated,
      props: {
        'vehicle_type': vehicleType,
        'city_id': cityId,
        'fare_bucket': amountBucket(fareEstimatePaise),
      },
    );
  }

  /// `stage` is one of `MopeduCancelStage.*`.
  void mopeduRideCancelled({required String stage}) {
    emit(
      MopeduTelemetryEvents.rideCancelled,
      props: {'stage': stage},
    );
  }

  void mopeduRideCompleted({
    required String vehicleType,
    required String cityId,
    required int finalFarePaise,
  }) {
    emit(
      MopeduTelemetryEvents.rideCompleted,
      props: {
        'vehicle_type': vehicleType,
        'city_id': cityId,
        'fare_bucket': amountBucket(finalFarePaise),
      },
    );
  }

  /// `kind` is one of `home | work | school | hospital | recent`.
  void mopeduSavedPlaceAdded({required String kind}) {
    emit(
      MopeduTelemetryEvents.savedPlaceAdded,
      props: {'kind': kind},
    );
  }

  void mopeduCityChanged({required String cityId}) {
    emit(
      MopeduTelemetryEvents.cityChanged,
      props: {'city_id': cityId},
    );
  }

  // ── Sprint 2 — partner side ────────────────────────────────────────

  /// `stepName` is one of: type | profile | kyc | vehicle | vehicle_docs
  /// | plan | payment | verification.
  void mopeduPartnerOnboardingStep({required String stepName}) {
    emit(
      MopeduTelemetryEvents.partnerOnboardingStep,
      props: {'step': stepName},
    );
  }

  /// `planId` is non-PII (a stable backend id). Never log price.
  void mopeduPartnerSubscribed({required String planId}) {
    emit(
      MopeduTelemetryEvents.partnerSubscribed,
      props: {'plan_id': planId},
    );
  }

  void mopeduPartnerOnlineToggled({required bool isOnline}) {
    emit(
      MopeduTelemetryEvents.partnerOnlineToggled,
      props: {'is_online': isOnline},
    );
  }

  /// NEVER log offer_id / partner_id / ride_id. `vehicleType` is categorical.
  void mopeduPartnerOfferReceived({required String vehicleType}) {
    emit(
      MopeduTelemetryEvents.partnerOfferReceived,
      props: {'vehicle_type': vehicleType},
    );
  }

  void mopeduPartnerOfferAccepted({required String vehicleType}) {
    emit(
      MopeduTelemetryEvents.partnerOfferAccepted,
      props: {'vehicle_type': vehicleType},
    );
  }

  /// `reason` is a categorical bucket from `RideRejectReason.*`.
  void mopeduPartnerOfferRejected({required String reason}) {
    emit(
      MopeduTelemetryEvents.partnerOfferRejected,
      props: {'reason': reason},
    );
  }

  /// Fares bucketed via `amountBucket` — never raw paise.
  void mopeduPartnerRideCompleted({
    required String vehicleType,
    required int finalFarePaise,
  }) {
    emit(
      MopeduTelemetryEvents.partnerRideCompleted,
      props: {
        'vehicle_type': vehicleType,
        'fare_bucket': amountBucket(finalFarePaise),
      },
    );
  }

  void mopeduPartnerSubscriptionRenewed({required String planId}) {
    emit(
      MopeduTelemetryEvents.partnerSubscriptionRenewed,
      props: {'plan_id': planId},
    );
  }

  // ── Sprint 4 — partner polish ──────────────────────────────────────
  //
  // PRIVACY: never log earnings amounts, partner phones, document numbers,
  // or referral codes (the code is derived from partner_id and is therefore
  // PII-adjacent — keep it out of telemetry props).

  /// Renewal succeeded via wallet. Plan id is non-PII.
  void mopeduPartnerSubscriptionRenewedViaWallet({required String planId}) {
    emit(
      MopeduTelemetryEvents.partnerSubscriptionRenewedViaWallet,
      props: {'plan_id': planId},
    );
  }

  /// Renewal succeeded via UPI. Plan id is non-PII.
  void mopeduPartnerSubscriptionRenewedViaUpi({required String planId}) {
    emit(
      MopeduTelemetryEvents.partnerSubscriptionRenewedViaUpi,
      props: {'plan_id': planId},
    );
  }

  /// Auto-renew preference toggled.
  void mopeduPartnerAutoRenewToggled({required bool enabled}) {
    emit(
      MopeduTelemetryEvents.partnerAutoRenewToggled,
      props: {'enabled': enabled},
    );
  }

  /// Plan switched. Both plan ids are non-PII (stable backend ids).
  void mopeduPartnerSubscriptionSwitched({
    required String fromPlanId,
    required String toPlanId,
  }) {
    emit(
      MopeduTelemetryEvents.partnerSubscriptionSwitched,
      props: {
        'from_plan_id': fromPlanId,
        'to_plan_id': toPlanId,
      },
    );
  }

  /// GST invoice download tapped. `period` is `today | week | month`.
  void mopeduPartnerInvoiceRequested({required String period}) {
    emit(
      MopeduTelemetryEvents.partnerInvoiceRequested,
      props: {'period': period},
    );
  }

  /// Partner shared their referral link. `channel` is `whatsapp | copy | sms`.
  /// NEVER pass the referral code itself — it derives from partner_id.
  void mopeduPartnerReferralShared({required String channel}) {
    emit(
      MopeduTelemetryEvents.partnerReferralShared,
      props: {'channel': channel},
    );
  }

  /// Dashboard surfaced expiring-document banners. `count` is bounded.
  void mopeduPartnerDocsExpiringBannerShown({required int count}) {
    emit(
      MopeduTelemetryEvents.partnerDocsExpiringBannerShown,
      props: {'count': count},
    );
  }

  // ── Sprint 3 — customer-side safety ───────────────────────────────

  /// Tap on a safety surface entry point. No identifiers — this fires
  /// on every Safety Center open across customer and partner contexts.
  void mopeduSafetyOpened() => emit(MopeduTelemetryEvents.safetyOpened);

  /// SOS button confirm. NEVER pass lat/lng or ride_id — this event is
  /// purely a counter. Surface volume to T&S ops via the analytics
  /// pipeline; the SOS *event* itself goes through the rider service.
  void mopeduSafetySosTriggered() {
    emit(MopeduTelemetryEvents.safetySosTriggered);
  }

  /// Customer added or updated their trusted contact. NEVER pass phone,
  /// name, or relationship — only the act of setting one.
  void mopeduSafetyTrustedContactSet() {
    emit(MopeduTelemetryEvents.safetyTrustedContactSet);
  }

  /// Customer minted a share-ride token. NEVER pass ride_id or token.
  void mopeduSafetyShareRideCreated() {
    emit(MopeduTelemetryEvents.safetyShareRideCreated);
  }

  /// `category` is a categorical bucket from `ComplaintCategory.wire`.
  /// NEVER pass ride_id, description, or partner identifiers.
  void mopeduComplaintSubmitted({required String category}) {
    emit(
      MopeduTelemetryEvents.complaintSubmitted,
      props: {'category': category},
    );
  }

  // ── Internals ────────────────────────────────────────────────────────

  Map<String, Object?> _sanitize(Map<String, Object?> input) {
    final out = <String, Object?>{};
    input.forEach((key, value) {
      if (_bannedPropKeys.contains(key.toLowerCase())) {
        AppLogger.warn(
          'MopeduTelemetry dropped banned key "$key"',
          tag: 'MopeduTelemetry',
        );
        return;
      }
      if (value == null || value is num || value is bool || value is String) {
        out[key] = value;
      } else if (value is List) {
        out[key] = value
            .where((e) => e == null || e is num || e is bool || e is String)
            .toList();
      } else {
        AppLogger.warn(
          'MopeduTelemetry dropped unsupported type for key "$key"',
          tag: 'MopeduTelemetry',
        );
      }
    });
    return out;
  }

  Future<void> _flush() async {
    if (_flushing || _buffer.isEmpty) return;
    _flushing = true;
    final batch = _buffer.toList(growable: false);
    try {
      await _client.post(
        '/v1/analytics/events',
        data: {
          'events': batch.map((e) => e.toJson()).toList(),
        },
      );
      for (var i = 0; i < batch.length && _buffer.isNotEmpty; i++) {
        _buffer.removeFirst();
      }
    } catch (e) {
      AppLogger.warn(
        'MopeduTelemetry flush failed; will retry: $e',
        tag: 'MopeduTelemetry',
      );
    } finally {
      _flushing = false;
    }
  }

  Future<void> flushNow() => _flush();
}

final mopeduTelemetryProvider = Provider<MopeduTelemetry>((ref) {
  final client = ref.watch(apiClientProvider);
  final tel = MopeduTelemetry(client);
  tel.start();
  ref.onDispose(tel.stop);
  return tel;
});
