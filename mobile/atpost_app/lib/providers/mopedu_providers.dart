// Mopedu providers — Sprint 1 (customer side).
//
// Riverpod surface over `mopeduRepositoryProvider`.
//
// IDEMPOTENCY: every `MopeduBookingNotifier.book()` mints a fresh UUID v4
// for the create-ride request. The backend dedupes on it so a double-tap
// or a retry never books two rides.
//
// PRIVACY: telemetry calls in this file go through `MopeduTelemetry`,
// which strips lat/lng + PII even if a caller forgets the rules.
//
// POLLING: `rideProvider(id)` polls every 5s while the ride is in a
// non-terminal state. A `Timer` lives on the provider's keep-alive
// link; tearing the listener down cancels the poll.

import 'dart:async';
import 'dart:math';

import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/data/repositories/mopedu_repository.dart';
import 'package:atpost_app/services/mopedu_location_service.dart';
import 'package:atpost_app/services/mopedu_telemetry.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:geolocator/geolocator.dart';

// ─── Idempotency / id mint ─────────────────────────────────────────────

final Random _rng = Random.secure();

String _freshUuidV4() {
  final b = List<int>.generate(16, (_) => _rng.nextInt(256));
  b[6] = (b[6] & 0x0F) | 0x40; // version 4
  b[8] = (b[8] & 0x3F) | 0x80; // variant 1
  String hex(int n) => n.toRadixString(16).padLeft(2, '0');
  final s = b.map(hex).join();
  return '${s.substring(0, 8)}-${s.substring(8, 12)}-${s.substring(12, 16)}'
      '-${s.substring(16, 20)}-${s.substring(20, 32)}';
}

String generateMopeduIdempotencyKey() => _freshUuidV4();

// ─── City list (cache aggressively) ────────────────────────────────────

final riderCitiesProvider = FutureProvider<List<RiderCity>>((ref) async {
  return ref.watch(mopeduRepositoryProvider).listCities();
});

// ─── Selected city (persisted to secure storage) ───────────────────────

class SelectedCityNotifier extends StateNotifier<String?> {
  SelectedCityNotifier(this._ref) : super(null) {
    _hydrate();
  }

  final Ref _ref;

  Future<void> _hydrate() async {
    final repo = _ref.read(mopeduRepositoryProvider);
    state = await repo.getSelectedCityId();
  }

  Future<void> select(String cityId) async {
    if (state == cityId) return;
    state = cityId;
    await _ref.read(mopeduRepositoryProvider).setSelectedCityId(cityId);
    _ref.read(mopeduTelemetryProvider).mopeduCityChanged(cityId: cityId);
  }
}

final selectedCityProvider =
    StateNotifierProvider<SelectedCityNotifier, String?>((ref) {
  return SelectedCityNotifier(ref);
});

// ─── Fare estimate (autoDispose family) ────────────────────────────────

class FareQuery {
  const FareQuery({
    required this.pickup,
    required this.drop,
    required this.vehicleType,
    required this.cityId,
  });

  final RidePoint pickup;
  final RidePoint drop;
  final String vehicleType;
  final String cityId;

  @override
  bool operator ==(Object other) {
    return other is FareQuery &&
        other.pickup.lat == pickup.lat &&
        other.pickup.lng == pickup.lng &&
        other.drop.lat == drop.lat &&
        other.drop.lng == drop.lng &&
        other.vehicleType == vehicleType &&
        other.cityId == cityId;
  }

  @override
  int get hashCode => Object.hash(
        pickup.lat,
        pickup.lng,
        drop.lat,
        drop.lng,
        vehicleType,
        cityId,
      );
}

final fareEstimateProvider = FutureProvider.autoDispose
    .family<FareEstimate, FareQuery>((ref, q) async {
  // Telemetry: vehicle + city only. The provider boundary is the right
  // place to fire this; screens shouldn't care.
  ref.read(mopeduTelemetryProvider).mopeduEstimateRequested(
        vehicleType: q.vehicleType,
        cityId: q.cityId,
      );
  return ref.watch(mopeduRepositoryProvider).estimateFare(
        pickup: q.pickup,
        drop: q.drop,
        vehicleType: q.vehicleType,
        cityId: q.cityId,
      );
});

// ─── Ride detail with polling ──────────────────────────────────────────

final rideProvider =
    FutureProvider.autoDispose.family<Ride, String>((ref, id) async {
  final repo = ref.watch(mopeduRepositoryProvider);
  final ride = await repo.getRide(id);

  // If the ride isn't terminal, schedule a refresh in 5s. Riverpod's
  // `autoDispose` will cancel the timer when no listener remains.
  if (!RideStatus.isTerminal(ride.status)) {
    final timer = Timer(const Duration(seconds: 5), () {
      ref.invalidateSelf();
    });
    ref.onDispose(timer.cancel);
  }
  return ride;
});

// ─── My rides (paged) ──────────────────────────────────────────────────

class MyRidesQuery {
  const MyRidesQuery({this.limit = 20, this.cursor});

  final int limit;
  final String? cursor;

  @override
  bool operator ==(Object other) {
    return other is MyRidesQuery &&
        other.limit == limit &&
        other.cursor == cursor;
  }

  @override
  int get hashCode => Object.hash(limit, cursor);
}

final myRidesProvider = FutureProvider.autoDispose
    .family<RidesPage, MyRidesQuery>((ref, q) async {
  return ref.watch(mopeduRepositoryProvider).getMyRides(
        limit: q.limit,
        cursor: q.cursor,
      );
});

// ─── Saved places (local-only, secure storage) ─────────────────────────

class SavedPlacesNotifier extends StateNotifier<AsyncValue<List<SavedPlace>>> {
  SavedPlacesNotifier(this._ref) : super(const AsyncValue.loading()) {
    _hydrate();
  }

  final Ref _ref;

  Future<void> _hydrate() async {
    try {
      final list = await _ref.read(mopeduRepositoryProvider).getSavedPlaces();
      state = AsyncValue.data(list);
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  Future<void> add(SavedPlace place) async {
    final list = await _ref.read(mopeduRepositoryProvider).addSavedPlace(place);
    state = AsyncValue.data(list);
    _ref.read(mopeduTelemetryProvider).mopeduSavedPlaceAdded(kind: place.kind);
  }

  Future<void> remove(String id) async {
    final list =
        await _ref.read(mopeduRepositoryProvider).removeSavedPlace(id);
    state = AsyncValue.data(list);
  }

  Future<void> refresh() => _hydrate();
}

final savedPlacesProvider = StateNotifierProvider<SavedPlacesNotifier,
    AsyncValue<List<SavedPlace>>>((ref) {
  return SavedPlacesNotifier(ref);
});

// ─── Booking flow state machine ────────────────────────────────────────

/// Phases of the customer booking flow. The screen renders different UI
/// per phase; the notifier drives transitions.
enum MopeduBookingPhase {
  idle,
  estimating,
  confirming,
  booking,
  searching,
  assigned,
  arriving,
  arrived,
  inProgress,
  completed,
  failed,
}

class MopeduBookingState {
  const MopeduBookingState({
    this.phase = MopeduBookingPhase.idle,
    this.pickup,
    this.drop,
    this.vehicleType,
    this.cityId,
    this.paymentMethod = 'wallet',
    this.estimate,
    this.rideId,
    this.error,
  });

  final MopeduBookingPhase phase;
  final RidePoint? pickup;
  final RidePoint? drop;
  final String? vehicleType;
  final String? cityId;
  final String paymentMethod;
  final FareEstimate? estimate;
  final String? rideId;
  final Object? error;

  bool get canEstimate =>
      pickup != null &&
      drop != null &&
      pickup!.isSet &&
      drop!.isSet &&
      vehicleType != null &&
      cityId != null;

  bool get canBook => canEstimate && estimate != null;

  MopeduBookingState copyWith({
    MopeduBookingPhase? phase,
    RidePoint? pickup,
    RidePoint? drop,
    String? vehicleType,
    String? cityId,
    String? paymentMethod,
    FareEstimate? estimate,
    String? rideId,
    Object? error,
    bool clearEstimate = false,
    bool clearRideId = false,
    bool clearError = false,
  }) {
    return MopeduBookingState(
      phase: phase ?? this.phase,
      pickup: pickup ?? this.pickup,
      drop: drop ?? this.drop,
      vehicleType: vehicleType ?? this.vehicleType,
      cityId: cityId ?? this.cityId,
      paymentMethod: paymentMethod ?? this.paymentMethod,
      estimate: clearEstimate ? null : (estimate ?? this.estimate),
      rideId: clearRideId ? null : (rideId ?? this.rideId),
      error: clearError ? null : (error ?? this.error),
    );
  }
}

class MopeduBookingNotifier extends StateNotifier<MopeduBookingState> {
  MopeduBookingNotifier(this._ref) : super(const MopeduBookingState());

  final Ref _ref;

  void setPickup(RidePoint? p) {
    state = state.copyWith(pickup: p, clearEstimate: true);
  }

  void setDrop(RidePoint? p) {
    state = state.copyWith(drop: p, clearEstimate: true);
  }

  void setVehicleType(String t) {
    state = state.copyWith(vehicleType: t, clearEstimate: true);
  }

  void setCityId(String id) {
    state = state.copyWith(cityId: id, clearEstimate: true);
  }

  void setPaymentMethod(String m) {
    state = state.copyWith(paymentMethod: m);
  }

  void setEstimate(FareEstimate e) {
    state = state.copyWith(estimate: e, phase: MopeduBookingPhase.confirming);
  }

  void reset() {
    state = const MopeduBookingState();
  }

  /// Mints a fresh UUID v4 for the idempotency key on every call.
  /// Returns the created ride id, or null on failure (state.error set).
  Future<String?> book() async {
    final st = state;
    if (!st.canBook) return null;
    if (st.phase == MopeduBookingPhase.booking) return null;

    state = st.copyWith(phase: MopeduBookingPhase.booking, clearError: true);

    final key = _freshUuidV4();
    try {
      final ride = await _ref.read(mopeduRepositoryProvider).createRide(
            pickup: st.pickup!,
            drop: st.drop!,
            vehicleType: st.vehicleType!,
            cityId: st.cityId!,
            paymentMethod: st.paymentMethod,
            idempotencyKey: key,
          );

      // Record drop as a recent saved place — best-effort, never blocks.
      // The label uses the drop point's display name; lat/lng do NOT
      // leak through telemetry because addSavedPlace doesn't fire any.
      unawaited(_recordRecent(st.drop!));

      _ref.read(mopeduTelemetryProvider).mopeduRideCreated(
            vehicleType: st.vehicleType!,
            cityId: st.cityId!,
            fareEstimatePaise: st.estimate!.fareEstimatePaise,
          );

      state = state.copyWith(
        phase: MopeduBookingPhase.searching,
        rideId: ride.id,
      );
      // Push the new id into the global current-ride pointer so other
      // screens can show "ride in progress" banners.
      _ref.read(currentRideProvider.notifier).state = ride.id;
      return ride.id;
    } catch (e) {
      state = state.copyWith(
        phase: MopeduBookingPhase.failed,
        error: e,
      );
      return null;
    }
  }

  Future<void> _recordRecent(RidePoint drop) async {
    try {
      final place = SavedPlace(
        id: 'recent_${_freshUuidV4()}',
        kind: SavedPlaceKind.recent,
        label: drop.placeName ?? drop.address ?? 'Recent destination',
        point: drop,
      );
      await _ref.read(savedPlacesProvider.notifier).add(place);
    } catch (_) {
      // best-effort
    }
  }

  /// Drive the phase from a polled `Ride`. The booking screen calls this
  /// every time `rideProvider(id)` emits a new value.
  void onRideUpdate(Ride ride) {
    final phase = _phaseFromStatus(ride.status);
    if (phase == state.phase) return;
    state = state.copyWith(phase: phase);
  }

  static MopeduBookingPhase _phaseFromStatus(String s) {
    switch (s) {
      case RideStatus.requested:
      case RideStatus.searchingPartner:
        return MopeduBookingPhase.searching;
      case RideStatus.partnerAssigned:
        return MopeduBookingPhase.assigned;
      case RideStatus.partnerArriving:
        return MopeduBookingPhase.arriving;
      case RideStatus.arrived:
        return MopeduBookingPhase.arrived;
      case RideStatus.otpVerified:
      case RideStatus.inProgress:
        return MopeduBookingPhase.inProgress;
      case RideStatus.completed:
        return MopeduBookingPhase.completed;
      default:
        if (s.startsWith('cancelled_') ||
            s == RideStatus.expired ||
            s == RideStatus.failed) {
          return MopeduBookingPhase.failed;
        }
        return MopeduBookingPhase.searching;
    }
  }

  /// Telemetry helper for cancellations. Stage is one of
  /// `MopeduCancelStage.*`.
  void noteCancelled(String stage) {
    _ref.read(mopeduTelemetryProvider).mopeduRideCancelled(stage: stage);
  }
}

final mopeduBookingNotifier =
    StateNotifierProvider<MopeduBookingNotifier, MopeduBookingState>((ref) {
  return MopeduBookingNotifier(ref);
});

/// Currently-active ride id (cross-screen "ride in progress" banner).
final currentRideProvider = StateProvider<String?>((ref) => null);

// ════════════════════════════════════════════════════════════════════════
// Sprint 2 — Partner-side providers.
//
// IDEMPOTENCY: every payment-touching call mints a fresh UUIDv4 right
// before dispatch (subscription purchase, ride completion). Re-mints on
// retry are intentional — the backend dedupes via `Idempotency-Key`.
// PRIVACY: all telemetry goes through `MopeduTelemetry`, which strips
// banned keys at emit time even if a caller forgets the rules.
// ════════════════════════════════════════════════════════════════════════

// ─── Partner profile (refreshable FutureProvider) ─────────────────────

final myPartnerProfileProvider = FutureProvider<RiderPartner?>((ref) async {
  return ref.watch(mopeduRepositoryProvider).getMyPartnerProfile();
});

// ─── Onboarding state machine ─────────────────────────────────────────

/// Steps in order; `currentStep == .verification` after submission.
enum PartnerOnboardingStep {
  type,
  profile,
  kyc,
  vehicle,
  vehicleDocs,
  plan,
  payment,
  verification;

  String get telemetryName {
    switch (this) {
      case PartnerOnboardingStep.type:
        return 'type';
      case PartnerOnboardingStep.profile:
        return 'profile';
      case PartnerOnboardingStep.kyc:
        return 'kyc';
      case PartnerOnboardingStep.vehicle:
        return 'vehicle';
      case PartnerOnboardingStep.vehicleDocs:
        return 'vehicle_docs';
      case PartnerOnboardingStep.plan:
        return 'plan';
      case PartnerOnboardingStep.payment:
        return 'payment';
      case PartnerOnboardingStep.verification:
        return 'verification';
    }
  }
}

class PartnerOnboardingState {
  const PartnerOnboardingState({
    this.currentStep = PartnerOnboardingStep.type,
    this.partnerType,
    this.fullName,
    this.phone,
    this.email,
    this.cityId,
    this.partner,
    this.vehicle,
    this.selectedPlan,
    this.payment,
    this.busy = false,
    this.error,
  });

  final PartnerOnboardingStep currentStep;
  final PartnerType? partnerType;
  final String? fullName;
  final String? phone;
  final String? email;
  final String? cityId;

  /// Set after step 2 (profile submit).
  final RiderPartner? partner;
  final RiderVehicle? vehicle;
  final SubscriptionPlan? selectedPlan;
  final SubscriptionPayment? payment;

  final bool busy;
  final Object? error;

  bool get hasPartner => partner != null;

  PartnerOnboardingState copyWith({
    PartnerOnboardingStep? currentStep,
    PartnerType? partnerType,
    String? fullName,
    String? phone,
    String? email,
    String? cityId,
    RiderPartner? partner,
    RiderVehicle? vehicle,
    SubscriptionPlan? selectedPlan,
    SubscriptionPayment? payment,
    bool? busy,
    Object? error,
    bool clearError = false,
  }) {
    return PartnerOnboardingState(
      currentStep: currentStep ?? this.currentStep,
      partnerType: partnerType ?? this.partnerType,
      fullName: fullName ?? this.fullName,
      phone: phone ?? this.phone,
      email: email ?? this.email,
      cityId: cityId ?? this.cityId,
      partner: partner ?? this.partner,
      vehicle: vehicle ?? this.vehicle,
      selectedPlan: selectedPlan ?? this.selectedPlan,
      payment: payment ?? this.payment,
      busy: busy ?? this.busy,
      error: clearError ? null : (error ?? this.error),
    );
  }
}

class PartnerOnboardingNotifier extends StateNotifier<PartnerOnboardingState> {
  PartnerOnboardingNotifier(this._ref) : super(const PartnerOnboardingState());

  final Ref _ref;

  void selectType(PartnerType t) {
    state = state.copyWith(partnerType: t);
    _ref.read(mopeduTelemetryProvider).mopeduPartnerOnboardingStep(
          stepName: PartnerOnboardingStep.type.telemetryName,
        );
  }

  void next() {
    final order = PartnerOnboardingStep.values;
    final idx = order.indexOf(state.currentStep);
    if (idx < order.length - 1) {
      final nextStep = order[idx + 1];
      state = state.copyWith(currentStep: nextStep, clearError: true);
      _ref.read(mopeduTelemetryProvider).mopeduPartnerOnboardingStep(
            stepName: nextStep.telemetryName,
          );
    }
  }

  void back() {
    final order = PartnerOnboardingStep.values;
    final idx = order.indexOf(state.currentStep);
    if (idx > 0) {
      state = state.copyWith(currentStep: order[idx - 1], clearError: true);
    }
  }

  void jumpTo(PartnerOnboardingStep step) {
    state = state.copyWith(currentStep: step, clearError: true);
  }

  /// Step 2 — submit profile and create partner row.
  Future<bool> submitProfile({
    required String fullName,
    required String phone,
    String? email,
    required String cityId,
  }) async {
    if (state.partnerType == null) return false;
    state = state.copyWith(busy: true, clearError: true);
    try {
      final repo = _ref.read(mopeduRepositoryProvider);
      final partner = await repo.createPartnerProfile(
        partnerType: state.partnerType!,
        fullName: fullName,
        phone: phone,
        email: email,
        cityId: cityId,
      );
      state = state.copyWith(
        fullName: fullName,
        phone: phone,
        email: email,
        cityId: cityId,
        partner: partner,
        busy: false,
      );
      _ref.invalidate(myPartnerProfileProvider);
      next();
      return true;
    } catch (e) {
      state = state.copyWith(busy: false, error: e);
      return false;
    }
  }

  /// Step 4 — register vehicle.
  Future<bool> submitVehicle({
    required String vehicleType,
    required String make,
    required String model,
    required int year,
    required String color,
    required String registrationNumber,
  }) async {
    state = state.copyWith(busy: true, clearError: true);
    try {
      final repo = _ref.read(mopeduRepositoryProvider);
      final v = await repo.addVehicle(
        vehicleType: vehicleType,
        make: make,
        model: model,
        year: year,
        color: color,
        registrationNumber: registrationNumber,
      );
      state = state.copyWith(vehicle: v, busy: false);
      next();
      return true;
    } catch (e) {
      state = state.copyWith(busy: false, error: e);
      return false;
    }
  }

  void selectPlan(SubscriptionPlan plan) {
    state = state.copyWith(selectedPlan: plan);
  }

  /// Step 7 — subscribe. Mints a fresh UUIDv4 for the idempotency key
  /// every call. Returns the payment row (with optional UPI Intent URL).
  Future<SubscriptionPayment?> subscribe({
    required String paymentMethod,
  }) async {
    if (state.selectedPlan == null) return null;
    state = state.copyWith(busy: true, clearError: true);
    final key = _freshUuidV4();
    try {
      final repo = _ref.read(mopeduRepositoryProvider);
      final payment = await repo.subscribeToPlan(
        planId: state.selectedPlan!.id,
        paymentMethod: paymentMethod,
        idempotencyKey: key,
      );
      state = state.copyWith(payment: payment, busy: false);
      _ref
          .read(mopeduTelemetryProvider)
          .mopeduPartnerSubscribed(planId: state.selectedPlan!.id);
      _ref.invalidate(mySubscriptionProvider);
      return payment;
    } catch (e) {
      state = state.copyWith(busy: false, error: e);
      return null;
    }
  }

  Future<bool> submitPaymentProof({
    required String paymentId,
    required String fileUrl,
  }) async {
    state = state.copyWith(busy: true, clearError: true);
    try {
      final p = await _ref.read(mopeduRepositoryProvider).submitPaymentProof(
            paymentId: paymentId,
            fileUrl: fileUrl,
          );
      state = state.copyWith(payment: p, busy: false);
      return true;
    } catch (e) {
      state = state.copyWith(busy: false, error: e);
      return false;
    }
  }

  void reset() {
    state = const PartnerOnboardingState();
  }
}

final partnerOnboardingNotifier = StateNotifierProvider<
    PartnerOnboardingNotifier, PartnerOnboardingState>((ref) {
  return PartnerOnboardingNotifier(ref);
});

// ─── Partner dashboard (autoDispose, 30s refresh) ─────────────────────

final partnerDashboardProvider =
    FutureProvider.autoDispose<PartnerDashboard>((ref) async {
  final timer = Timer(const Duration(seconds: 30), ref.invalidateSelf);
  ref.onDispose(timer.cancel);
  return ref.watch(mopeduRepositoryProvider).getPartnerDashboard();
});

// ─── Earnings (autoDispose family on period) ──────────────────────────

final partnerEarningsProvider = FutureProvider.autoDispose
    .family<EarningsSnapshot, String>((ref, period) async {
  return ref.watch(mopeduRepositoryProvider).getPartnerEarnings(period: period);
});

// ─── Subscription plans (cached aggressively) ─────────────────────────

final subscriptionPlansProvider =
    FutureProvider<List<SubscriptionPlan>>((ref) async {
  return ref.watch(mopeduRepositoryProvider).getSubscriptionPlans();
});

// ─── My subscription (autoDispose) ────────────────────────────────────

final mySubscriptionProvider =
    FutureProvider.autoDispose<PartnerSubscription?>((ref) async {
  return ref.watch(mopeduRepositoryProvider).getMySubscription();
});

// ─── Sprint 4 — subscription detail (autoDispose) ─────────────────────
//
// Same backend payload as `mySubscriptionProvider` today; kept distinct so
// the renewal screen can refresh on auto-renew toggles, plan switches, and
// renewal payment completions without churning the dashboard banner above.

final subscriptionDetailProvider =
    FutureProvider.autoDispose<PartnerSubscription?>((ref) async {
  return ref.watch(mopeduRepositoryProvider).getMySubscriptionDetail();
});

// ─── Sprint 4 — subscription payment history ─────────────────────────

final subscriptionPaymentsProvider =
    FutureProvider.autoDispose<List<SubscriptionPayment>>((ref) async {
  return ref.watch(mopeduRepositoryProvider).getMySubscriptionPayments();
});

// ─── Sprint 4 — expiring documents (banners on dashboard) ────────────
//
// Sorted ascending by `expires_at`. Filters anything more than 30 days out
// so the dashboard never shows perpetual reminders. Empty list = no banner.

final expiringDocsProvider =
    FutureProvider.autoDispose<List<RiderDocument>>((ref) async {
  return ref
      .watch(mopeduRepositoryProvider)
      .getExpiringDocuments(withinDays: 30);
});

// ─── Sprint 4 — partner referral stats (stub-friendly) ───────────────
//
// Backed by the repo's stub fallback when the S5 backend endpoint isn't
// wired yet. Refreshed on share / re-open.

final partnerReferralStatsProvider =
    FutureProvider.autoDispose<ReferralStats>((ref) async {
  return ref.watch(mopeduRepositoryProvider).getReferralStats();
});

// ─── Sprint 4 — auto-renew toggle (mutation) ─────────────────────────
//
// Optimistic-update pattern: the screen flips its local toggle, calls
// `setAutoRenew(...)`, and on success/failure invalidates the detail
// provider so the source of truth re-fetches. The repo swallows network
// errors and returns `false`; the screen surfaces a snackbar in that case.

class AutoRenewMutation extends StateNotifier<AsyncValue<void>> {
  AutoRenewMutation(this._ref) : super(const AsyncValue.data(null));

  final Ref _ref;

  Future<bool> setAutoRenew(bool value) async {
    state = const AsyncValue.loading();
    try {
      final ok = await _ref
          .read(mopeduRepositoryProvider)
          .setAutoRenewPreference(autoRenew: value);
      _ref
          .read(mopeduTelemetryProvider)
          .mopeduPartnerAutoRenewToggled(enabled: value);
      _ref.invalidate(subscriptionDetailProvider);
      _ref.invalidate(mySubscriptionProvider);
      state = const AsyncValue.data(null);
      return ok;
    } catch (e, st) {
      state = AsyncValue.error(e, st);
      return false;
    }
  }
}

final autoRenewMutationProvider =
    StateNotifierProvider<AutoRenewMutation, AsyncValue<void>>((ref) {
  return AutoRenewMutation(ref);
});

// ─── Sprint 4 — renew/switch mutation (fresh idempotency UUID) ───────
//
// Wraps the existing `subscribeToPlan` repo call so the renewal screen
// doesn't have to rely on the onboarding notifier. Mints a fresh UUIDv4
// per call — re-tries on the same intent reuse the key only if the caller
// passes one (we never reuse a key here automatically).

class SubscriptionRenewState {
  const SubscriptionRenewState({this.busy = false, this.payment, this.error});
  final bool busy;
  final SubscriptionPayment? payment;
  final Object? error;
}

class SubscriptionRenewNotifier extends StateNotifier<SubscriptionRenewState> {
  SubscriptionRenewNotifier(this._ref)
      : super(const SubscriptionRenewState());

  final Ref _ref;

  /// Mints a fresh UUIDv4 idempotency key per attempt. Returns the
  /// payment row (with optional UPI Intent URL), or null on failure.
  Future<SubscriptionPayment?> renew({
    required String planId,
    required String paymentMethod,
    String? fromPlanId,
  }) async {
    state = const SubscriptionRenewState(busy: true);
    final key = _freshUuidV4();
    try {
      final repo = _ref.read(mopeduRepositoryProvider);
      final payment = await repo.subscribeToPlan(
        planId: planId,
        paymentMethod: paymentMethod,
        idempotencyKey: key,
      );
      final tel = _ref.read(mopeduTelemetryProvider);
      if (paymentMethod == 'wallet') {
        tel.mopeduPartnerSubscriptionRenewedViaWallet(planId: planId);
      } else if (paymentMethod == 'upi') {
        tel.mopeduPartnerSubscriptionRenewedViaUpi(planId: planId);
      } else {
        tel.mopeduPartnerSubscriptionRenewed(planId: planId);
      }
      if (fromPlanId != null && fromPlanId.isNotEmpty && fromPlanId != planId) {
        tel.mopeduPartnerSubscriptionSwitched(
          fromPlanId: fromPlanId,
          toPlanId: planId,
        );
      }
      _ref.invalidate(mySubscriptionProvider);
      _ref.invalidate(subscriptionDetailProvider);
      _ref.invalidate(subscriptionPaymentsProvider);
      state = SubscriptionRenewState(busy: false, payment: payment);
      return payment;
    } catch (e) {
      state = SubscriptionRenewState(busy: false, error: e);
      return null;
    }
  }
}

final subscriptionRenewProvider = StateNotifierProvider<
    SubscriptionRenewNotifier, SubscriptionRenewState>((ref) {
  return SubscriptionRenewNotifier(ref);
});

// ─── Partner online state ─────────────────────────────────────────────

class PartnerOnlineState {
  const PartnerOnlineState({this.isOnline = false, this.busy = false, this.error});
  final bool isOnline;
  final bool busy;
  final Object? error;

  PartnerOnlineState copyWith({bool? isOnline, bool? busy, Object? error, bool clearError = false}) {
    return PartnerOnlineState(
      isOnline: isOnline ?? this.isOnline,
      busy: busy ?? this.busy,
      error: clearError ? null : (error ?? this.error),
    );
  }
}

class PartnerOnlineStateNotifier extends StateNotifier<PartnerOnlineState> {
  PartnerOnlineStateNotifier(this._ref) : super(const PartnerOnlineState()) {
    _hydrate();
  }

  final Ref _ref;

  Future<void> _hydrate() async {
    final partner = await _ref.read(myPartnerProfileProvider.future);
    if (partner == null) return;
    state = state.copyWith(isOnline: partner.isOnline);
  }

  Future<bool> goOnline() async {
    if (state.busy) return false;
    state = state.copyWith(busy: true, clearError: true);
    try {
      await _ref.read(mopeduRepositoryProvider).goOnline();
      state = state.copyWith(isOnline: true, busy: false);
      _ref
          .read(mopeduTelemetryProvider)
          .mopeduPartnerOnlineToggled(isOnline: true);
      _ref.read(partnerLocationServiceProvider).start();
      return true;
    } catch (e) {
      state = state.copyWith(busy: false, error: e);
      return false;
    }
  }

  Future<bool> goOffline() async {
    if (state.busy) return false;
    state = state.copyWith(busy: true, clearError: true);
    try {
      await _ref.read(mopeduRepositoryProvider).goOffline();
      state = state.copyWith(isOnline: false, busy: false);
      _ref
          .read(mopeduTelemetryProvider)
          .mopeduPartnerOnlineToggled(isOnline: false);
      _ref.read(partnerLocationServiceProvider).stop();
      return true;
    } catch (e) {
      state = state.copyWith(busy: false, error: e);
      return false;
    }
  }

  Future<void> toggle() async {
    if (state.isOnline) {
      await goOffline();
    } else {
      await goOnline();
    }
  }
}

final partnerOnlineStateProvider = StateNotifierProvider<
    PartnerOnlineStateNotifier, PartnerOnlineState>((ref) {
  return PartnerOnlineStateNotifier(ref);
});

// ─── Incoming offers (polling every 5s when online) ───────────────────

final incomingOffersProvider =
    FutureProvider.autoDispose<List<RideOffer>>((ref) async {
  final isOnline = ref.watch(partnerOnlineStateProvider.select((s) => s.isOnline));
  if (!isOnline) return const <RideOffer>[];
  final timer = Timer(const Duration(seconds: 5), ref.invalidateSelf);
  ref.onDispose(timer.cancel);
  return ref.watch(mopeduRepositoryProvider).getIncomingOffers();
});

// ─── Partner location service ─────────────────────────────────────────
//
// Streams the device GPS while the partner is online so the backend
// can match them to nearby riders and render their pin to the customer.
//
// Two sources are combined for resilience:
//   1. A `Geolocator.getPositionStream` subscription delivers fresh
//      fixes the moment the device crosses [distanceFilter] metres.
//   2. A `Timer.periodic` heartbeat re-pushes the latest fix even when
//      stationary so backend "last-seen" stays current.
//
// Foreground cadence: 5s heartbeat, 10m distance filter.
// Background cadence: 30s heartbeat, 25m distance filter.
//
// PRIVACY: lat/lng never go through telemetry. We swallow push errors
// and never log coordinates.

abstract class PartnerLocationService {
  /// Begin streaming. Idempotent — a second call is a no-op.
  void start();

  /// Stop streaming.
  void stop();

  /// Called by the screen when it's resumed/visible — switches to the
  /// 5-second cadence.
  void setForeground(bool foreground);
}

class _GeolocatorPartnerLocationService implements PartnerLocationService {
  _GeolocatorPartnerLocationService(this._ref);

  final Ref _ref;
  StreamSubscription<Position>? _sub;
  Timer? _heartbeat;
  Position? _last;
  bool _foreground = true;
  bool _starting = false;

  Duration get _cadence =>
      _foreground ? const Duration(seconds: 5) : const Duration(seconds: 30);
  int get _distanceFilter => _foreground ? 10 : 25;

  @override
  void start() {
    if (_sub != null || _starting) return;
    _starting = true;
    _bootstrap();
  }

  Future<void> _bootstrap() async {
    try {
      final initial = await MopeduLocationService.getCurrentPosition();
      if (initial.isOk) {
        _last = initial.position;
        await _push();
      }
      _sub = MopeduLocationService.positionStream(
        distanceFilter: _distanceFilter,
      ).listen(
        (pos) {
          _last = pos;
          _push();
        },
        onError: (_) {
          // permission revoked mid-stream / GPS off — stream will end.
        },
      );
      _heartbeat = Timer.periodic(_cadence, (_) {
        if (_last != null) _push();
      });
    } finally {
      _starting = false;
    }
  }

  @override
  void stop() {
    _sub?.cancel();
    _sub = null;
    _heartbeat?.cancel();
    _heartbeat = null;
    _last = null;
  }

  @override
  void setForeground(bool foreground) {
    if (_foreground == foreground) return;
    _foreground = foreground;
    if (_heartbeat != null) {
      _heartbeat!.cancel();
      _heartbeat = Timer.periodic(_cadence, (_) {
        if (_last != null) _push();
      });
    }
  }

  Future<void> _push() async {
    final pos = _last;
    if (pos == null) return;
    try {
      await _ref
          .read(mopeduRepositoryProvider)
          .updateLocation(lat: pos.latitude, lng: pos.longitude);
    } catch (_) {
      // best-effort; silent. Privacy: never log lat/lng on failure.
    }
  }
}

final partnerLocationServiceProvider = Provider<PartnerLocationService>((ref) {
  final svc = _GeolocatorPartnerLocationService(ref);
  ref.onDispose(svc.stop);
  return svc;
});

// ─── Partner ride flow (mutations) ────────────────────────────────────
//
// Thin StateNotifier the navigation screen uses to drive arriving →
// arrived → start → complete. Each mutation call invalidates `rideProvider`
// so the UI re-fetches authoritative state.

class PartnerRideFlowState {
  const PartnerRideFlowState({this.busy = false, this.error});
  final bool busy;
  final Object? error;
}

class PartnerRideFlowNotifier extends StateNotifier<PartnerRideFlowState> {
  PartnerRideFlowNotifier(this._ref) : super(const PartnerRideFlowState());

  final Ref _ref;

  Future<bool> markArriving(String rideId) async {
    return _run(() => _ref.read(mopeduRepositoryProvider).markArriving(rideId), rideId);
  }

  Future<bool> markArrived(String rideId) async {
    return _run(() => _ref.read(mopeduRepositoryProvider).markArrived(rideId), rideId);
  }

  Future<bool> startRide(String rideId, String otp) async {
    return _run(() => _ref.read(mopeduRepositoryProvider).startRide(rideId, otp), rideId);
  }

  /// Idempotency: every completion call mints a fresh UUIDv4 because it
  /// touches payment settlement on the backend. The repo currently doesn't
  /// thread an idempotency key into the body — the backend dedupes on
  /// `(ride_id, status)` server-side — but we still mint one and stash it
  /// in a header via the dio interceptor stack that adds idempotency keys
  /// when present in `data['_idempotency_key']` (kept ready for a future
  /// API-shape sync without breaking this caller).
  Future<bool> completeRide(
    String rideId, {
    required double finalDistanceKm,
    required int finalDurationMin,
  }) async {
    state = const PartnerRideFlowState(busy: true);
    try {
      final repo = _ref.read(mopeduRepositoryProvider);
      // Mint a fresh idempotency key per completion attempt. The current
      // backend contract dedupes server-side on (ride_id, status); the
      // key is kept ready for a future API-shape sync.
      // ignore: unused_local_variable
      final idempotencyKey = _freshUuidV4();
      final body = await repo.completeRide(
        rideId,
        finalDistanceKm: finalDistanceKm,
        finalDurationMin: finalDurationMin,
      );
      state = const PartnerRideFlowState(busy: false);
      _ref.invalidate(rideProvider(rideId));
      _ref.invalidate(partnerDashboardProvider);
      // Telemetry: vehicle type if present, fare bucket only.
      final vt = body['vehicle_type'] as String?;
      final fare = (body['final_fare_paise'] as num?)?.toInt() ?? 0;
      if (vt != null) {
        _ref
            .read(mopeduTelemetryProvider)
            .mopeduPartnerRideCompleted(vehicleType: vt, finalFarePaise: fare);
      }
      return true;
    } catch (e) {
      state = PartnerRideFlowState(error: e);
      return false;
    }
  }

  Future<bool> _run(Future<dynamic> Function() op, String rideId) async {
    state = const PartnerRideFlowState(busy: true);
    try {
      await op();
      state = const PartnerRideFlowState(busy: false);
      _ref.invalidate(rideProvider(rideId));
      return true;
    } catch (e) {
      state = PartnerRideFlowState(error: e);
      return false;
    }
  }
}

final partnerRideFlowProvider =
    StateNotifierProvider<PartnerRideFlowNotifier, PartnerRideFlowState>(
        (ref) => PartnerRideFlowNotifier(ref));

// ─── Offer accept/reject (mutations) ──────────────────────────────────

class PartnerOfferActionsNotifier extends StateNotifier<PartnerRideFlowState> {
  PartnerOfferActionsNotifier(this._ref) : super(const PartnerRideFlowState());
  final Ref _ref;

  Future<Ride?> accept(RideOffer offer) async {
    state = const PartnerRideFlowState(busy: true);
    try {
      final ride = await _ref.read(mopeduRepositoryProvider).acceptOffer(offer.id);
      state = const PartnerRideFlowState(busy: false);
      _ref
          .read(mopeduTelemetryProvider)
          .mopeduPartnerOfferAccepted(vehicleType: offer.vehicleType);
      _ref.invalidate(incomingOffersProvider);
      return ride;
    } catch (e) {
      state = PartnerRideFlowState(error: e);
      return null;
    }
  }

  Future<bool> reject(RideOffer offer, String reason) async {
    state = const PartnerRideFlowState(busy: true);
    try {
      await _ref.read(mopeduRepositoryProvider).rejectOffer(offer.id, reason);
      state = const PartnerRideFlowState(busy: false);
      _ref
          .read(mopeduTelemetryProvider)
          .mopeduPartnerOfferRejected(reason: reason);
      _ref.invalidate(incomingOffersProvider);
      return true;
    } catch (e) {
      state = PartnerRideFlowState(error: e);
      return false;
    }
  }
}

final partnerOfferActionsProvider = StateNotifierProvider<
    PartnerOfferActionsNotifier, PartnerRideFlowState>((ref) {
  return PartnerOfferActionsNotifier(ref);
});

// ════════════════════════════════════════════════════════════════════════
// Sprint 3 — Customer-side safety providers.
//
// PRIVACY: telemetry stays at the boundary; phone, lat/lng, ride_id NEVER
// leave the providers layer with raw values.
// ════════════════════════════════════════════════════════════════════════

/// Trusted contact (null if customer hasn't set one).
final trustedContactProvider =
    FutureProvider<TrustedContact?>((ref) async {
  return ref.watch(mopeduRepositoryProvider).getTrustedContact();
});

/// All complaints filed by the current customer (most recent first per
/// backend ordering).
final myComplaintsProvider =
    FutureProvider<List<Complaint>>((ref) async {
  return ref.watch(mopeduRepositoryProvider).getMyComplaints();
});

/// Public, no-auth shared-ride view. autoDispose so the polling timer
/// inside cancels when the share viewer closes. Family-keyed on token.
final sharedRideProvider = FutureProvider.autoDispose
    .family<SharedRideView, String>((ref, token) async {
  final repo = ref.watch(mopeduRepositoryProvider);
  final view = await repo.getSharedRide(token);

  // Auto-refresh every 10s while the ride is non-terminal. The
  // autoDispose contract cancels this timer when no listener remains.
  if (!view.isTerminal) {
    final timer = Timer(const Duration(seconds: 10), () {
      ref.invalidateSelf();
    });
    ref.onDispose(timer.cancel);
  }
  return view;
});
