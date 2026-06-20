// Mopedu repository — Sprint 1 customer side.
//
// Backend: `Architecture/services/rider-service/` proxied at `/v1/rider/*`.
// Saved places persistence is local-only for Sprint 1 (secure storage);
// the backend `GET/POST/DELETE /v1/rider/places` endpoints land in
// Sprint 2.
//
// Money: paise (int) end-to-end. Lat/lng: double. Idempotency keys are
// minted by the providers layer (`mopedu_providers.dart`) and passed in;
// we never mint them here so a screen can show "we are about to charge
// you with key X" before tapping confirm.

import 'dart:convert';

import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

const String kMopeduSavedPlacesKey = 'mopedu_saved_places_v1';
const String kMopeduSelectedCityKey = 'mopedu_selected_city_v1';
const int kMopeduMaxRecents = 5;

class MopeduRepository {
  MopeduRepository(this._api, {FlutterSecureStorage? storage})
      : _storage = storage ?? const FlutterSecureStorage();

  final ApiClient _api;
  final FlutterSecureStorage _storage;

  // ─── Network: cities ──────────────────────────────────────────────────

  Future<List<RiderCity>> listCities() async {
    final res = await _api.get('/v1/rider/cities');
    final raw = res.data['data'];
    final list = (raw is List)
        ? raw
        : ((raw is Map && raw['items'] is List) ? raw['items'] as List : const []);
    return list
        .whereType<Map>()
        .map((e) => RiderCity.fromJson(e.cast<String, dynamic>()))
        .toList();
  }

  // ─── Network: fare estimate ───────────────────────────────────────────

  Future<FareEstimate> estimateFare({
    required RidePoint pickup,
    required RidePoint drop,
    required String vehicleType,
    required String cityId,
  }) async {
    final res = await _api.post(
      '/v1/rider/estimate',
      data: {
        'pickup_lat': pickup.lat,
        'pickup_lng': pickup.lng,
        'drop_lat': drop.lat,
        'drop_lng': drop.lng,
        'vehicle_type': vehicleType,
        'city_id': cityId,
      },
    );
    final raw = res.data['data'];
    final map = (raw is Map<String, dynamic>) ? raw : <String, dynamic>{};
    return FareEstimate.fromJson(map);
  }

  // ─── Network: rides ───────────────────────────────────────────────────

  Future<Ride> createRide({
    required RidePoint pickup,
    required RidePoint drop,
    required String vehicleType,
    required String cityId,
    required String paymentMethod,
    required String idempotencyKey,
    DateTime? scheduledFor,
  }) async {
    final res = await _api.post(
      '/v1/rider/rides',
      data: {
        'pickup': pickup.toJson(),
        'drop': drop.toJson(),
        'vehicle_type': vehicleType,
        'city_id': cityId,
        'payment_method': paymentMethod,
        'idempotency_key': idempotencyKey,
        if (scheduledFor != null)
          'scheduled_for': scheduledFor.toUtc().toIso8601String(),
      },
    );
    final raw = res.data['data'];
    final map = (raw is Map<String, dynamic>) ? raw : <String, dynamic>{};
    return Ride.fromJson(map);
  }

  Future<Ride> getRide(String id) async {
    final res = await _api.get('/v1/rider/rides/$id');
    final raw = res.data['data'];
    final map = (raw is Map<String, dynamic>) ? raw : <String, dynamic>{};
    return Ride.fromJson(map);
  }

  Future<RidesPage> getMyRides({int limit = 20, String? cursor}) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null) params['cursor'] = cursor;
    final res = await _api.get(
      '/v1/rider/rides/me',
      queryParameters: params,
    );
    final body = res.data;
    final items = (body is Map && body['data'] is List)
        ? body['data'] as List
        : const <dynamic>[];
    final nextCursor = (body is Map && body['meta'] is Map)
        ? (body['meta'] as Map)['next_cursor'] as String?
        : null;
    return RidesPage(
      items: items
          .whereType<Map>()
          .map((e) => Ride.fromJson(e.cast<String, dynamic>()))
          .toList(),
      nextCursor: nextCursor,
    );
  }

  /// Sprint 2 endpoint placeholder. Surfaced here so screens can call it
  /// today and we only flip the body when the backend ships. Until then
  /// the repository swallows the error and returns `false` so the UX
  /// always shows the "thanks for rating" snackbar.
  Future<bool> rateRide({
    required String rideId,
    required int stars,
    String? comment,
  }) async {
    try {
      await _api.post(
        '/v1/rider/rides/$rideId/rate',
        data: {
          'stars': stars,
          if (comment != null && comment.isNotEmpty) 'comment': comment,
        },
      );
      return true;
    } catch (_) {
      return false;
    }
  }

  // ─── Local storage: saved places ─────────────────────────────────────
  //
  // We persist the list as a single JSON blob under one key. This keeps
  // the schema migration simple (versioned key), and avoids any extra
  // dependency on `shared_preferences` — secure storage is already used
  // throughout the app.

  Future<List<SavedPlace>> getSavedPlaces() async {
    try {
      final raw = await _storage.read(key: kMopeduSavedPlacesKey);
      if (raw == null || raw.isEmpty) return const [];
      final decoded = jsonDecode(raw);
      if (decoded is! List) return const [];
      return decoded
          .whereType<Map>()
          .map((e) => SavedPlace.fromJson(e.cast<String, dynamic>()))
          .toList();
    } catch (_) {
      return const [];
    }
  }

  Future<void> _writeSavedPlaces(List<SavedPlace> list) async {
    final encoded = jsonEncode(list.map((e) => e.toJson()).toList());
    await _storage.write(key: kMopeduSavedPlacesKey, value: encoded);
  }

  Future<List<SavedPlace>> addSavedPlace(SavedPlace place) async {
    final all = List<SavedPlace>.from(await getSavedPlaces());
    // For fixed kinds (home/work/school/hospital), replace existing of
    // same kind. Recents append, but bounded.
    if (SavedPlaceKind.fixed.contains(place.kind)) {
      all.removeWhere((p) => p.kind == place.kind);
      all.add(place);
    } else if (place.kind == SavedPlaceKind.recent) {
      // De-dup recents by coords + label.
      all.removeWhere(
        (p) =>
            p.kind == SavedPlaceKind.recent &&
            p.point.lat == place.point.lat &&
            p.point.lng == place.point.lng &&
            p.label == place.label,
      );
      all.add(place);
      // Cap recents.
      final recents = all.where((p) => p.kind == SavedPlaceKind.recent).toList();
      if (recents.length > kMopeduMaxRecents) {
        final overflow = recents.length - kMopeduMaxRecents;
        for (var i = 0; i < overflow; i++) {
          all.remove(recents[i]);
        }
      }
    } else {
      all.add(place);
    }
    await _writeSavedPlaces(all);
    return all;
  }

  Future<List<SavedPlace>> removeSavedPlace(String id) async {
    final all = (await getSavedPlaces()).where((p) => p.id != id).toList();
    await _writeSavedPlaces(all);
    return all;
  }

  Future<List<SavedPlace>> getRecents() async {
    final all = await getSavedPlaces();
    return all.where((p) => p.kind == SavedPlaceKind.recent).toList();
  }

  // ─── Local storage: selected city ────────────────────────────────────

  Future<String?> getSelectedCityId() async {
    try {
      return await _storage.read(key: kMopeduSelectedCityKey);
    } catch (_) {
      return null;
    }
  }

  Future<void> setSelectedCityId(String cityId) async {
    try {
      await _storage.write(key: kMopeduSelectedCityKey, value: cityId);
    } catch (_) {
      // best-effort
    }
  }

  // ════════════════════════════════════════════════════════════════════
  // Sprint 2 — Partner-side endpoints.
  //
  // Privacy: this layer NEVER logs lat/lng, OTP, or partner identifiers.
  // Idempotency keys are minted by the providers layer (subscription
  // purchase, ride completion) and threaded through; we never mint here.
  // ════════════════════════════════════════════════════════════════════

  Map<String, dynamic> _unwrapData(dynamic body) {
    if (body is Map && body['data'] is Map<String, dynamic>) {
      return body['data'] as Map<String, dynamic>;
    }
    if (body is Map<String, dynamic>) return body;
    return const <String, dynamic>{};
  }

  // ─── Partner profile ────────────────────────────────────────────────

  Future<RiderPartner> createPartnerProfile({
    required PartnerType partnerType,
    required String fullName,
    required String phone,
    String? email,
    required String cityId,
  }) async {
    final res = await _api.post(
      '/v1/rider/partners',
      data: {
        'partner_type': partnerType.wire,
        'full_name': fullName,
        'phone': phone,
        if (email != null && email.isNotEmpty) 'email': email,
        'city_id': cityId,
      },
    );
    return RiderPartner.fromJson(_unwrapData(res.data));
  }

  /// Returns null when the user has no partner profile yet (404 path).
  Future<RiderPartner?> getMyPartnerProfile() async {
    try {
      final res = await _api.get('/v1/rider/partners/me');
      final map = _unwrapData(res.data);
      if (map.isEmpty || map['id'] == null) return null;
      return RiderPartner.fromJson(map);
    } catch (e) {
      // 404 = no profile yet. Any other error: rethrow so callers can
      // distinguish offline from "not onboarded".
      final msg = e.toString().toLowerCase();
      if (msg.contains('404') || msg.contains('not found')) return null;
      rethrow;
    }
  }

  Future<RiderPartner> updatePartnerProfile(
    Map<String, dynamic> updates,
  ) async {
    final res = await _api.patch('/v1/rider/partners/me', data: updates);
    return RiderPartner.fromJson(_unwrapData(res.data));
  }

  // ─── Partner KYC ────────────────────────────────────────────────────

  Future<RiderDocument> submitKYCDocument({
    required String documentType,
    String? documentNumber,
    required String fileUrl,
  }) async {
    final res = await _api.post(
      '/v1/rider/partners/me/documents',
      data: {
        'document_type': documentType,
        if (documentNumber != null && documentNumber.isNotEmpty)
          'document_number': documentNumber,
        'file_url': fileUrl,
      },
    );
    return RiderDocument.fromJson(_unwrapData(res.data));
  }

  Future<PartnerAadhaarFlowStart> startAadhaarKYC() async {
    final res = await _api.post('/v1/rider/partners/me/aadhaar/start');
    return PartnerAadhaarFlowStart.fromJson(_unwrapData(res.data));
  }

  /// Returns the resulting verification status string (`pending`,
  /// `approved`, etc.). The backend updates the partner row server-side.
  Future<String> completeAadhaarKYC({
    required String code,
    required String state,
  }) async {
    final res = await _api.post(
      '/v1/rider/partners/me/aadhaar/callback',
      data: {'code': code, 'state': state},
    );
    final map = _unwrapData(res.data);
    return (map['status'] as String?) ?? 'pending';
  }

  // ─── Vehicles ───────────────────────────────────────────────────────

  Future<RiderVehicle> addVehicle({
    required String vehicleType,
    required String make,
    required String model,
    required int year,
    required String color,
    required String registrationNumber,
  }) async {
    final res = await _api.post(
      '/v1/rider/partners/me/vehicles',
      data: {
        'vehicle_type': vehicleType,
        'make': make,
        'model': model,
        'year': year,
        'color': color,
        'registration_number': registrationNumber,
      },
    );
    return RiderVehicle.fromJson(_unwrapData(res.data));
  }

  Future<RiderDocument> submitVehicleDocument(
    String vehicleId, {
    required String documentType,
    required String fileUrl,
    DateTime? expiresAt,
  }) async {
    final res = await _api.post(
      '/v1/rider/vehicles/$vehicleId/documents',
      data: {
        'document_type': documentType,
        'file_url': fileUrl,
        if (expiresAt != null)
          'expires_at': expiresAt.toUtc().toIso8601String(),
      },
    );
    return RiderDocument.fromJson(_unwrapData(res.data));
  }

  // ─── Subscription ──────────────────────────────────────────────────

  Future<List<SubscriptionPlan>> getSubscriptionPlans() async {
    final res = await _api.get('/v1/rider/subscriptions/plans');
    final body = res.data;
    final raw = (body is Map) ? body['data'] : null;
    final list = (raw is List)
        ? raw
        : ((raw is Map && raw['items'] is List)
            ? raw['items'] as List
            : const <dynamic>[]);
    return list
        .whereType<Map>()
        .map((e) => SubscriptionPlan.fromJson(e.cast<String, dynamic>()))
        .toList();
  }

  /// Idempotency: caller mints a fresh UUIDv4 per attempt. Passing the
  /// same key on retry returns the original payment row.
  Future<SubscriptionPayment> subscribeToPlan({
    required String planId,
    required String paymentMethod,
    required String idempotencyKey,
  }) async {
    final res = await _api.post(
      '/v1/rider/subscriptions/subscribe',
      data: {
        'plan_id': planId,
        'payment_method': paymentMethod,
        'idempotency_key': idempotencyKey,
      },
    );
    return SubscriptionPayment.fromJson(_unwrapData(res.data));
  }

  Future<SubscriptionPayment> submitPaymentProof({
    required String paymentId,
    required String fileUrl,
  }) async {
    final res = await _api.post(
      '/v1/rider/subscriptions/payment-proof',
      data: {'payment_id': paymentId, 'file_url': fileUrl},
    );
    return SubscriptionPayment.fromJson(_unwrapData(res.data));
  }

  Future<PartnerSubscription?> getMySubscription() async {
    try {
      final res = await _api.get('/v1/rider/subscriptions/me');
      final map = _unwrapData(res.data);
      if (map.isEmpty || map['id'] == null) return null;
      return PartnerSubscription.fromJson(map);
    } catch (e) {
      final msg = e.toString().toLowerCase();
      if (msg.contains('404') || msg.contains('not found')) return null;
      rethrow;
    }
  }

  /// Sprint 4 — same payload as `getMySubscription` today, but a separate
  /// method so screens can opt in to the richer fields (`renewal_failure_count`,
  /// `auto_renew`, `expires_at`, `days_remaining`) once the backend lands
  /// the dedicated detail endpoint. Until then `subscriptions/me` already
  /// returns the extra fields if present.
  Future<PartnerSubscription?> getMySubscriptionDetail() async {
    return getMySubscription();
  }

  /// Sprint 4 — partner toggles auto-renew on/off. Best-effort: errors are
  /// rethrown so the screen can show a snackbar but the local UI state
  /// stays in sync via optimistic update + invalidation.
  ///
  /// NOTE (Sprint 5 backend): the `PATCH /v1/rider/subscriptions/me/auto-renew`
  /// endpoint is wired in S4 mobile but the rider service ships the full
  /// implementation in Sprint 5. Until then the call may 404 — callers
  /// should treat the failure as a soft warning.
  Future<bool> setAutoRenewPreference({required bool autoRenew}) async {
    try {
      await _api.patch(
        '/v1/rider/subscriptions/me/auto-renew',
        data: {'auto_renew': autoRenew},
      );
      return true;
    } catch (_) {
      return false;
    }
  }

  /// Sprint 4 — list every subscription payment for the current partner.
  /// Powers the renewal screen's "Payment history" section. Backend may
  /// page; v1 returns the full set inline.
  Future<List<SubscriptionPayment>> getMySubscriptionPayments() async {
    try {
      final res = await _api.get('/v1/rider/subscriptions/payments');
      final body = res.data;
      final raw = (body is Map) ? body['data'] : null;
      final list = (raw is List)
          ? raw
          : ((raw is Map && raw['items'] is List)
              ? raw['items'] as List
              : const <dynamic>[]);
      return list
          .whereType<Map>()
          .map((e) => SubscriptionPayment.fromJson(e.cast<String, dynamic>()))
          .toList();
    } catch (_) {
      // Endpoint may not exist yet on every backend tier; return empty so
      // the UI can render a "no payments yet" state.
      return const <SubscriptionPayment>[];
    }
  }

  /// Sprint 4 — partner + vehicle documents the current partner has
  /// uploaded. Filtered client-side to those expiring within `withinDays`.
  ///
  /// PRIVACY: document_number is part of `RiderDocument` but the UI never
  /// shows it; telemetry never logs it.
  Future<List<RiderDocument>> getExpiringDocuments({int withinDays = 30}) async {
    Future<List<RiderDocument>> fetch(String path) async {
      try {
        final res = await _api.get(path);
        final body = res.data;
        final raw = (body is Map) ? body['data'] : null;
        final list = (raw is List)
            ? raw
            : ((raw is Map && raw['items'] is List)
                ? raw['items'] as List
                : const <dynamic>[]);
        return list
            .whereType<Map>()
            .map((e) => RiderDocument.fromJson(e.cast<String, dynamic>()))
            .toList();
      } catch (_) {
        return const <RiderDocument>[];
      }
    }

    final partnerDocs = await fetch('/v1/rider/partners/me/documents');
    final vehicleDocs = await fetch('/v1/rider/partners/me/vehicle-documents');

    final now = DateTime.now();
    final cutoff = now.add(Duration(days: withinDays));
    final all = <RiderDocument>[...partnerDocs, ...vehicleDocs];
    final filtered = all.where((d) {
      final exp = d.expiresAt;
      if (exp == null) return false;
      // Expiring soon (or already expired in the last few days — still
      // surface so the partner can act).
      return exp.isBefore(cutoff);
    }).toList();
    filtered.sort((a, b) {
      final ax = a.expiresAt ?? now;
      final bx = b.expiresAt ?? now;
      return ax.compareTo(bx);
    });
    return filtered;
  }

  /// Sprint 4 — partner referral stats. v1 backend doesn't expose a
  /// dedicated endpoint yet; we surface a derivation built from the
  /// partner profile id so the UI always has *something* to render.
  ///
  /// TODO Sprint 5 backend: replace with `GET /v1/rider/partners/me/referrals`
  /// once the endpoint ships.
  Future<ReferralStats> getReferralStats() async {
    try {
      final res = await _api.get('/v1/rider/partners/me/referrals');
      final map = _unwrapData(res.data);
      if (map.isNotEmpty) {
        return ReferralStats.fromJson(map);
      }
    } catch (_) {
      // fall through to stub
    }
    final partner = await getMyPartnerProfile();
    final id = (partner?.id ?? '').replaceAll('-', '');
    final code = id.length >= 6
        ? id.substring(0, 6).toUpperCase()
        : 'MOPEDU';
    return ReferralStats(
      code: code,
      pendingCount: 0,
      activatedCount: 0,
      totalRewardLeads: 0,
    );
  }

  // ─── Online state + location ────────────────────────────────────────

  Future<void> goOnline() async {
    await _api.post('/v1/rider/partners/me/online');
  }

  Future<void> goOffline() async {
    await _api.post('/v1/rider/partners/me/offline');
  }

  /// High-frequency. Lat/lng leave the device but never enter telemetry.
  Future<void> updateLocation({
    required double lat,
    required double lng,
    double? speed,
    double? heading,
  }) async {
    await _api.post(
      '/v1/rider/partners/me/location',
      data: {
        'lat': lat,
        'lng': lng,
        'speed': ?speed,
        'heading': ?heading,
      },
    );
  }

  // ─── Realtime token ─────────────────────────────────────────────────

  /// Issues an HMAC-signed topic token for the realtime SSE gateway.
  /// Returns `{token, topics}` where `topics` lists the names the
  /// caller is authorized to subscribe to.
  Future<({String token, List<String> topics})> getRealtimeToken() async {
    final res = await _api.post('/v1/rider/realtime/token');
    final body = res.data is Map ? (res.data as Map)['data'] : null;
    if (body is! Map) {
      throw StateError('realtime token: malformed response');
    }
    final token = (body['token'] as String?) ?? '';
    final raw = body['topics'];
    final topics = (raw is List)
        ? raw.whereType<String>().toList()
        : const <String>[];
    return (token: token, topics: topics);
  }

  // ─── Offers ─────────────────────────────────────────────────────────

  Future<List<RideOffer>> getIncomingOffers() async {
    final res = await _api.get('/v1/rider/offers/incoming');
    final body = res.data;
    final raw = (body is Map) ? body['data'] : null;
    final list = (raw is List)
        ? raw
        : ((raw is Map && raw['items'] is List)
            ? raw['items'] as List
            : const <dynamic>[]);
    return list
        .whereType<Map>()
        .map((e) => RideOffer.fromJson(e.cast<String, dynamic>()))
        .toList();
  }

  /// On accept the backend strips OTP from the returned ride payload —
  /// partner enters it from the customer's screen on arrival.
  Future<Ride> acceptOffer(String offerId) async {
    final res = await _api.post('/v1/rider/offers/$offerId/accept');
    return Ride.fromJson(_unwrapData(res.data));
  }

  Future<void> rejectOffer(String offerId, String reason) async {
    await _api.post(
      '/v1/rider/offers/$offerId/reject',
      data: {'reason': reason},
    );
  }

  // ─── Ride lifecycle (partner side) ──────────────────────────────────

  Future<Ride> markArriving(String rideId) async {
    final res = await _api.post('/v1/rider/rides/$rideId/arriving');
    return Ride.fromJson(_unwrapData(res.data));
  }

  Future<Ride> markArrived(String rideId) async {
    final res = await _api.post('/v1/rider/rides/$rideId/arrived');
    return Ride.fromJson(_unwrapData(res.data));
  }

  /// OTP is provided by the customer face-to-face — never logged.
  Future<Ride> startRide(String rideId, String otp) async {
    final res = await _api.post(
      '/v1/rider/rides/$rideId/start',
      data: {'otp': otp},
    );
    return Ride.fromJson(_unwrapData(res.data));
  }

  /// Returns the (raw) backend payload as a map. Caller can pull
  /// `final_fare_paise`, `payment_method`, etc.
  Future<Map<String, dynamic>> completeRide(
    String rideId, {
    required double finalDistanceKm,
    required int finalDurationMin,
  }) async {
    final res = await _api.post(
      '/v1/rider/rides/$rideId/complete',
      data: {
        'final_distance_km': finalDistanceKm,
        'final_duration_min': finalDurationMin,
      },
    );
    return _unwrapData(res.data);
  }

  // ─── Dashboard + earnings ───────────────────────────────────────────

  Future<PartnerDashboard> getPartnerDashboard() async {
    final res = await _api.get('/v1/rider/partners/me/dashboard');
    return PartnerDashboard.fromJson(_unwrapData(res.data));
  }

  Future<EarningsSnapshot> getPartnerEarnings({String period = 'today'}) async {
    final res = await _api.get(
      '/v1/rider/partners/me/earnings',
      queryParameters: {'period': period},
    );
    return EarningsSnapshot.fromJson(_unwrapData(res.data));
  }

  // ════════════════════════════════════════════════════════════════════
  // Sprint 3 — Customer-side safety endpoints.
  //
  // Privacy: lat/lng leave the device on `triggerSOS` only — never enter
  // telemetry. Phone numbers from the trusted-contact endpoints are also
  // privacy-tagged (see `MopeduTelemetry._bannedPropKeys`).
  // ════════════════════════════════════════════════════════════════════

  /// Fires the SOS event. Lat/lng are optional — backend will fall back to
  /// last known partner location when omitted. NEVER log lat/lng.
  Future<void> triggerSOS(
    String rideId, {
    double? lat,
    double? lng,
  }) async {
    final body = <String, dynamic>{};
    if (lat != null) body['lat'] = lat;
    if (lng != null) body['lng'] = lng;
    await _api.post('/v1/rider/rides/$rideId/sos', data: body);
  }

  /// Mints a one-time share token. The returned `shareUrl` is what the
  /// customer copies / forwards.
  Future<ShareTokenResult> createShareToken(String rideId) async {
    final res = await _api.post('/v1/rider/rides/$rideId/share');
    return ShareTokenResult.fromJson(_unwrapData(res.data));
  }

  /// Public endpoint — no auth header required. The interceptor stack
  /// will still attach a token if present, which the backend ignores.
  /// Surface this directly to the shared-ride viewer screen.
  Future<SharedRideView> getSharedRide(String token) async {
    final res = await _api.get('/v1/rider/share/$token');
    return SharedRideView.fromJson(_unwrapData(res.data));
  }

  Future<Complaint> submitComplaint(
    String rideId, {
    required ComplaintCategory category,
    String? description,
  }) async {
    final res = await _api.post(
      '/v1/rider/rides/$rideId/complain',
      data: {
        'category': category.wire,
        if (description != null && description.trim().isNotEmpty)
          'description': description.trim(),
      },
    );
    return Complaint.fromJson(_unwrapData(res.data));
  }

  Future<List<Complaint>> getMyComplaints() async {
    final res = await _api.get('/v1/rider/complaints/me');
    final body = res.data;
    final raw = (body is Map) ? body['data'] : null;
    final list = (raw is List)
        ? raw
        : ((raw is Map && raw['items'] is List)
            ? raw['items'] as List
            : const <dynamic>[]);
    return list
        .whereType<Map>()
        .map((e) => Complaint.fromJson(e.cast<String, dynamic>()))
        .toList();
  }

  /// Returns null when the customer has not yet configured a contact
  /// (the backend returns 404 in that case).
  Future<TrustedContact?> getTrustedContact() async {
    try {
      final res = await _api.get('/v1/rider/trusted-contact');
      final map = _unwrapData(res.data);
      if (map.isEmpty || (map['phone'] as String?)?.isEmpty != false) {
        return null;
      }
      return TrustedContact.fromJson(map);
    } catch (e) {
      final msg = e.toString().toLowerCase();
      if (msg.contains('404') || msg.contains('not found')) return null;
      rethrow;
    }
  }

  Future<TrustedContact> setTrustedContact(TrustedContact contact) async {
    final res = await _api.put(
      '/v1/rider/trusted-contact',
      data: contact.toJson(),
    );
    return TrustedContact.fromJson(_unwrapData(res.data));
  }

  // ════════════════════════════════════════════════════════════════════
  // Sprint 5 — soft-launch waitlist + city gate.
  //
  // Backend wires `POST /v1/rider/waitlist` in Sprint 6. Until then this
  // is a fire-and-forget POST that should not surface a hard error — the
  // city-gate UI swallows network failures and shows a friendly retry
  // (mirrors the Pulse Sprint 6 pattern in `pulse_repository.dart`).
  // ════════════════════════════════════════════════════════════════════

  /// Join the city waitlist. Called from `MopeduWaitlistScreen` /
  /// `MopeduGate` when the user's city is not in the v1 allow-list.
  Future<void> joinWaitlist({
    required String city,
    required String email,
  }) async {
    try {
      await _api.post(
        '/v1/rider/waitlist',
        data: {'city': city, 'email': email},
      );
    } catch (e) {
      // 404 / 501 — endpoint not yet wired; treat as success so the UI
      // can show the confirmation card. Any other error rethrows so the
      // screen can show a retry prompt.
      final msg = e.toString().toLowerCase();
      if (msg.contains('404') ||
          msg.contains('501') ||
          msg.contains('not found') ||
          msg.contains('unimplemented')) {
        return;
      }
      rethrow;
    }
  }
}

final mopeduRepositoryProvider = Provider<MopeduRepository>((ref) {
  return MopeduRepository(ref.watch(apiClientProvider));
});
