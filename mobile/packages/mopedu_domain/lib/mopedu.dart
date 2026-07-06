// Mopedu — rider mini-app data models (Sprint 1 customer side).
//
// Source spec: `mopedu/MOPEDU_SPEC.md` §9 (ride states), §10 (vehicle types).
// Backend contract: `Architecture/services/rider-service/` exposed via
// `/v1/rider/*`.
//
// CONVENTIONS:
//   * snake_case JSON ↔ camelCase Dart, single `fromJson` per class.
//   * Money is paise (int) — never doubles. Display via `formatRupees`.
//   * Lat/lng kept as `double` here. NEVER pass to telemetry; the privacy
//     contract forbids logging coordinates.
//   * Status strings stay as raw strings, not enums — the backend may add
//     terminal states (e.g., new cancellation reasons) without breaking
//     the client. Use the helpers on `RideStatus` for branching.

/// Ride state catalog (per spec §9). Strings, not an enum, because the
/// backend can add new states without breaking older clients.
class RideStatus {
  RideStatus._();

  static const requested = 'requested';
  static const searchingPartner = 'searching_partner';
  static const partnerAssigned = 'partner_assigned';
  static const partnerArriving = 'partner_arriving';
  static const arrived = 'arrived';
  static const otpVerified = 'otp_verified';
  static const inProgress = 'in_progress';
  static const completed = 'completed';
  static const cancelledByCustomer = 'cancelled_by_customer';
  static const cancelledByPartner = 'cancelled_by_partner';
  static const cancelledBySystem = 'cancelled_by_system';
  static const expired = 'expired';
  static const failed = 'failed';

  /// Terminal = no more polling needed.
  static bool isTerminal(String s) {
    return s == completed ||
        s == expired ||
        s == failed ||
        s.startsWith('cancelled_');
  }

  /// "Active ride" = the customer should see the booking-in-progress UI.
  static bool isActive(String s) {
    return s == requested ||
        s == searchingPartner ||
        s == partnerAssigned ||
        s == partnerArriving ||
        s == arrived ||
        s == otpVerified ||
        s == inProgress;
  }
}

/// Vehicle types offered to customers in v1.
/// `scheduled` and `ev_*` exist in the backend schema but are skipped in
/// the v1 customer UI per the implementation plan.
class VehicleType {
  VehicleType._();

  static const bike = 'bike';
  static const auto = 'auto';
  static const miniCab = 'mini_cab';
  static const sedan = 'sedan';
  static const suv = 'suv';
  static const premium = 'premium';

  static const all = <String>[bike, auto, miniCab, sedan, suv, premium];

  static String label(String t) {
    switch (t) {
      case bike:
        return 'Bike';
      case auto:
        return 'Auto';
      case miniCab:
        return 'Mini cab';
      case sedan:
        return 'Sedan';
      case suv:
        return 'SUV';
      case premium:
        return 'Premium';
      default:
        return t;
    }
  }
}

class RiderCity {
  const RiderCity({
    required this.id,
    required this.name,
    required this.state,
    required this.country,
    required this.currencyCode,
    required this.isActive,
  });

  final String id;
  final String name;
  final String state;
  final String country;
  final String currencyCode;
  final bool isActive;

  factory RiderCity.fromJson(Map<String, dynamic> json) {
    return RiderCity(
      id: json['id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      state: json['state'] as String? ?? '',
      country: json['country'] as String? ?? 'IN',
      currencyCode: json['currency_code'] as String? ?? 'INR',
      isActive: json['is_active'] as bool? ?? true,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'state': state,
        'country': country,
        'currency_code': currencyCode,
        'is_active': isActive,
      };
}

class FareEstimate {
  const FareEstimate({
    required this.estimatedDistanceKm,
    required this.estimatedDurationMin,
    required this.fareEstimatePaise,
    required this.surgeMultiplier,
    required this.vehicleType,
    required this.etaToPickupSeconds,
  });

  final double estimatedDistanceKm;
  final int estimatedDurationMin;
  final int fareEstimatePaise;
  final double surgeMultiplier;
  final String vehicleType;
  final int etaToPickupSeconds;

  factory FareEstimate.fromJson(Map<String, dynamic> json) {
    return FareEstimate(
      estimatedDistanceKm:
          (json['estimated_distance_km'] as num?)?.toDouble() ?? 0,
      estimatedDurationMin: (json['estimated_duration_min'] as num?)?.toInt() ?? 0,
      fareEstimatePaise: (json['fare_estimate_paise'] as num?)?.toInt() ?? 0,
      surgeMultiplier: (json['surge_multiplier'] as num?)?.toDouble() ?? 1.0,
      vehicleType: json['vehicle_type'] as String? ?? '',
      etaToPickupSeconds:
          (json['eta_to_pickup_seconds'] as num?)?.toInt() ?? 0,
    );
  }

  Map<String, dynamic> toJson() => {
        'estimated_distance_km': estimatedDistanceKm,
        'estimated_duration_min': estimatedDurationMin,
        'fare_estimate_paise': fareEstimatePaise,
        'surge_multiplier': surgeMultiplier,
        'vehicle_type': vehicleType,
        'eta_to_pickup_seconds': etaToPickupSeconds,
      };
}

class RidePoint {
  const RidePoint({
    required this.lat,
    required this.lng,
    this.address,
    this.placeName,
  });

  final double lat;
  final double lng;
  final String? address;
  final String? placeName;

  factory RidePoint.fromJson(Map<String, dynamic> json) {
    return RidePoint(
      lat: (json['lat'] as num?)?.toDouble() ?? 0,
      lng: (json['lng'] as num?)?.toDouble() ?? 0,
      address: json['address'] as String?,
      placeName: json['place_name'] as String?,
    );
  }

  Map<String, dynamic> toJson() => {
        'lat': lat,
        'lng': lng,
        if (address != null) 'address': address,
        if (placeName != null) 'place_name': placeName,
      };

  /// Display string for the pickup/drop pill. Prefers place name, falls
  /// back to address, then to coordinates.
  String get displayName {
    if (placeName != null && placeName!.isNotEmpty) return placeName!;
    if (address != null && address!.isNotEmpty) return address!;
    return '${lat.toStringAsFixed(4)}, ${lng.toStringAsFixed(4)}';
  }

  bool get isSet => lat != 0 || lng != 0;
}

class Ride {
  const Ride({
    required this.id,
    required this.customerId,
    required this.status,
    required this.vehicleType,
    required this.cityId,
    required this.pickup,
    required this.drop,
    required this.paymentMethod,
    required this.fareEstimatePaise,
    this.finalFarePaise,
    this.partnerId,
    this.partnerName,
    this.partnerPhotoUrl,
    this.vehicleNumber,
    this.partnerRating,
    this.otp,
    this.otpVerified = false,
    required this.requestedAt,
    this.assignedAt,
    this.startedAt,
    this.completedAt,
  });

  final String id;
  final String customerId;
  final String status;
  final String vehicleType;
  final String cityId;
  final RidePoint pickup;
  final RidePoint drop;
  final String paymentMethod;
  final int fareEstimatePaise;
  final int? finalFarePaise;
  final String? partnerId;
  final String? partnerName;
  final String? partnerPhotoUrl;
  final String? vehicleNumber;
  final double? partnerRating;
  final String? otp;
  final bool otpVerified;
  final DateTime requestedAt;
  final DateTime? assignedAt;
  final DateTime? startedAt;
  final DateTime? completedAt;

  factory Ride.fromJson(Map<String, dynamic> json) {
    DateTime? parseTs(dynamic v) {
      if (v == null) return null;
      if (v is String && v.isEmpty) return null;
      try {
        return DateTime.parse(v as String);
      } catch (_) {
        return null;
      }
    }

    final pickupRaw = json['pickup'];
    final dropRaw = json['drop'];

    return Ride(
      id: json['id'] as String? ?? '',
      customerId: json['customer_id'] as String? ?? '',
      status: json['status'] as String? ?? RideStatus.requested,
      vehicleType: json['vehicle_type'] as String? ?? '',
      cityId: json['city_id'] as String? ?? '',
      pickup: pickupRaw is Map<String, dynamic>
          ? RidePoint.fromJson(pickupRaw)
          : const RidePoint(lat: 0, lng: 0),
      drop: dropRaw is Map<String, dynamic>
          ? RidePoint.fromJson(dropRaw)
          : const RidePoint(lat: 0, lng: 0),
      paymentMethod: json['payment_method'] as String? ?? 'wallet',
      fareEstimatePaise: (json['fare_estimate_paise'] as num?)?.toInt() ?? 0,
      finalFarePaise: (json['final_fare_paise'] as num?)?.toInt(),
      partnerId: json['partner_id'] as String?,
      partnerName: json['partner_name'] as String?,
      partnerPhotoUrl: json['partner_photo_url'] as String?,
      vehicleNumber: json['vehicle_number'] as String?,
      partnerRating: (json['partner_rating'] as num?)?.toDouble(),
      otp: json['otp'] as String?,
      otpVerified: json['otp_verified'] as bool? ?? false,
      requestedAt: parseTs(json['requested_at']) ?? DateTime.now(),
      assignedAt: parseTs(json['assigned_at']),
      startedAt: parseTs(json['started_at']),
      completedAt: parseTs(json['completed_at']),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'customer_id': customerId,
        'status': status,
        'vehicle_type': vehicleType,
        'city_id': cityId,
        'pickup': pickup.toJson(),
        'drop': drop.toJson(),
        'payment_method': paymentMethod,
        'fare_estimate_paise': fareEstimatePaise,
        if (finalFarePaise != null) 'final_fare_paise': finalFarePaise,
        if (partnerId != null) 'partner_id': partnerId,
        if (partnerName != null) 'partner_name': partnerName,
        if (partnerPhotoUrl != null) 'partner_photo_url': partnerPhotoUrl,
        if (vehicleNumber != null) 'vehicle_number': vehicleNumber,
        if (partnerRating != null) 'partner_rating': partnerRating,
        if (otp != null) 'otp': otp,
        'otp_verified': otpVerified,
        'requested_at': requestedAt.toUtc().toIso8601String(),
        if (assignedAt != null)
          'assigned_at': assignedAt!.toUtc().toIso8601String(),
        if (startedAt != null)
          'started_at': startedAt!.toUtc().toIso8601String(),
        if (completedAt != null)
          'completed_at': completedAt!.toUtc().toIso8601String(),
      };
}

class RidesPage {
  const RidesPage({required this.items, this.nextCursor});

  final List<Ride> items;
  final String? nextCursor;
}

/// Saved place kinds. Strings (not enum) so we can persist them as raw
/// JSON without an extra codec layer.
class SavedPlaceKind {
  SavedPlaceKind._();

  static const home = 'home';
  static const work = 'work';
  static const school = 'school';
  static const hospital = 'hospital';
  static const recent = 'recent';

  static const fixed = <String>[home, work, school, hospital];

  static String label(String k) {
    switch (k) {
      case home:
        return 'Home';
      case work:
        return 'Work';
      case school:
        return 'School';
      case hospital:
        return 'Hospital';
      case recent:
        return 'Recent';
      default:
        return k;
    }
  }
}

class SavedPlace {
  const SavedPlace({
    required this.id,
    required this.kind,
    required this.label,
    required this.point,
  });

  final String id;
  final String kind;
  final String label;
  final RidePoint point;

  factory SavedPlace.fromJson(Map<String, dynamic> json) {
    return SavedPlace(
      id: json['id'] as String? ?? '',
      kind: json['kind'] as String? ?? SavedPlaceKind.recent,
      label: json['label'] as String? ?? '',
      point: RidePoint.fromJson(
        (json['point'] as Map?)?.cast<String, dynamic>() ??
            const <String, dynamic>{},
      ),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'kind': kind,
        'label': label,
        'point': point.toJson(),
      };
}

// ════════════════════════════════════════════════════════════════════════
// Sprint 2 — Partner-side models.
//
// Spec: `mopedu/MOPEDU_SPEC.md` §6 (partner onboarding), §7 (subscription),
// §11 (offer flow), §13 (privacy contract).
//
// Convention recap: snake_case JSON ↔ camelCase Dart, every class has a
// fromJson + toJson. Money is paise (int). Enums are Dart enums but are
// serialised as their wire string for forward-compat with backend additions.
// ════════════════════════════════════════════════════════════════════════

/// Partner kind picked at onboarding step 1. Backend stores the wire
/// string; the enum exists so the UI doesn't typo `'individual_driver'`.
enum PartnerType {
  individualDriver,
  ownerDriver,
  fleetOwner,
  fleetDriver;

  String get wire {
    switch (this) {
      case PartnerType.individualDriver:
        return 'individual_driver';
      case PartnerType.ownerDriver:
        return 'owner_driver';
      case PartnerType.fleetOwner:
        return 'fleet_owner';
      case PartnerType.fleetDriver:
        return 'fleet_driver';
    }
  }

  String get label {
    switch (this) {
      case PartnerType.individualDriver:
        return 'Individual driver';
      case PartnerType.ownerDriver:
        return 'Owner-driver';
      case PartnerType.fleetOwner:
        return 'Fleet owner';
      case PartnerType.fleetDriver:
        return 'Fleet driver';
    }
  }

  String get description {
    switch (this) {
      case PartnerType.individualDriver:
        return 'Drive any rented vehicle on Mopedu.';
      case PartnerType.ownerDriver:
        return 'You own and drive your own vehicle.';
      case PartnerType.fleetOwner:
        return 'You own multiple vehicles; drivers report to you.';
      case PartnerType.fleetDriver:
        return 'You drive a vehicle owned by a Mopedu fleet partner.';
    }
  }

  static PartnerType fromWire(String? s) {
    switch (s) {
      case 'individual_driver':
        return PartnerType.individualDriver;
      case 'owner_driver':
        return PartnerType.ownerDriver;
      case 'fleet_owner':
        return PartnerType.fleetOwner;
      case 'fleet_driver':
        return PartnerType.fleetDriver;
      default:
        return PartnerType.individualDriver;
    }
  }
}

/// Verification state per document / per vehicle.
enum VerificationStatus {
  draft,
  pending,
  approved,
  rejected,
  expired;

  String get wire {
    switch (this) {
      case VerificationStatus.draft:
        return 'draft';
      case VerificationStatus.pending:
        return 'pending';
      case VerificationStatus.approved:
        return 'approved';
      case VerificationStatus.rejected:
        return 'rejected';
      case VerificationStatus.expired:
        return 'expired';
    }
  }

  String get label {
    switch (this) {
      case VerificationStatus.draft:
        return 'Draft';
      case VerificationStatus.pending:
        return 'Under review';
      case VerificationStatus.approved:
        return 'Approved';
      case VerificationStatus.rejected:
        return 'Rejected';
      case VerificationStatus.expired:
        return 'Expired';
    }
  }

  static VerificationStatus fromWire(String? s) {
    switch (s) {
      case 'draft':
        return VerificationStatus.draft;
      case 'pending':
        return VerificationStatus.pending;
      case 'approved':
        return VerificationStatus.approved;
      case 'rejected':
        return VerificationStatus.rejected;
      case 'expired':
        return VerificationStatus.expired;
      default:
        return VerificationStatus.draft;
    }
  }
}

/// Lifecycle of a partner profile on the platform.
enum PartnerStatus {
  draft,
  pendingVerification,
  approved,
  suspended,
  blocked,
  inactive;

  String get wire {
    switch (this) {
      case PartnerStatus.draft:
        return 'draft';
      case PartnerStatus.pendingVerification:
        return 'pending_verification';
      case PartnerStatus.approved:
        return 'approved';
      case PartnerStatus.suspended:
        return 'suspended';
      case PartnerStatus.blocked:
        return 'blocked';
      case PartnerStatus.inactive:
        return 'inactive';
    }
  }

  String get label {
    switch (this) {
      case PartnerStatus.draft:
        return 'Draft';
      case PartnerStatus.pendingVerification:
        return 'Pending verification';
      case PartnerStatus.approved:
        return 'Approved';
      case PartnerStatus.suspended:
        return 'Suspended';
      case PartnerStatus.blocked:
        return 'Blocked';
      case PartnerStatus.inactive:
        return 'Inactive';
    }
  }

  bool get canDrive => this == PartnerStatus.approved;

  static PartnerStatus fromWire(String? s) {
    switch (s) {
      case 'draft':
        return PartnerStatus.draft;
      case 'pending_verification':
        return PartnerStatus.pendingVerification;
      case 'approved':
        return PartnerStatus.approved;
      case 'suspended':
        return PartnerStatus.suspended;
      case 'blocked':
        return PartnerStatus.blocked;
      case 'inactive':
        return PartnerStatus.inactive;
      default:
        return PartnerStatus.draft;
    }
  }
}

/// Subscription status for a partner. `gracePeriod` lets a partner keep
/// taking rides for 3 days after expiry; `expired` blocks new offers.
enum SubscriptionStatus {
  trial,
  active,
  gracePeriod,
  expired,
  cancelled,
  suspended;

  String get wire {
    switch (this) {
      case SubscriptionStatus.trial:
        return 'trial';
      case SubscriptionStatus.active:
        return 'active';
      case SubscriptionStatus.gracePeriod:
        return 'grace_period';
      case SubscriptionStatus.expired:
        return 'expired';
      case SubscriptionStatus.cancelled:
        return 'cancelled';
      case SubscriptionStatus.suspended:
        return 'suspended';
    }
  }

  String get label {
    switch (this) {
      case SubscriptionStatus.trial:
        return 'Trial';
      case SubscriptionStatus.active:
        return 'Active';
      case SubscriptionStatus.gracePeriod:
        return 'Grace period';
      case SubscriptionStatus.expired:
        return 'Expired';
      case SubscriptionStatus.cancelled:
        return 'Cancelled';
      case SubscriptionStatus.suspended:
        return 'Suspended';
    }
  }

  bool get canTakeRides =>
      this == SubscriptionStatus.trial ||
      this == SubscriptionStatus.active ||
      this == SubscriptionStatus.gracePeriod;

  static SubscriptionStatus fromWire(String? s) {
    switch (s) {
      case 'trial':
        return SubscriptionStatus.trial;
      case 'active':
        return SubscriptionStatus.active;
      case 'grace_period':
        return SubscriptionStatus.gracePeriod;
      case 'expired':
        return SubscriptionStatus.expired;
      case 'cancelled':
        return SubscriptionStatus.cancelled;
      case 'suspended':
        return SubscriptionStatus.suspended;
      default:
        return SubscriptionStatus.expired;
    }
  }
}

/// Document type strings (kept as a `String` constant set, not an enum,
/// because the backend can extend without breaking older clients).
class PartnerDocumentType {
  PartnerDocumentType._();

  static const aadhaar = 'aadhaar';
  static const pan = 'pan';
  static const drivingLicense = 'driving_license';
  static const profilePhoto = 'profile_photo';
  static const policeVerification = 'police_verification';

  static String label(String t) {
    switch (t) {
      case aadhaar:
        return 'Aadhaar';
      case pan:
        return 'PAN';
      case drivingLicense:
        return 'Driving licence';
      case profilePhoto:
        return 'Profile photo';
      case policeVerification:
        return 'Police verification';
      default:
        return t;
    }
  }
}

class VehicleDocumentType {
  VehicleDocumentType._();

  static const rc = 'rc';
  static const insurance = 'insurance';
  static const pollutionCert = 'pollution_cert';
  static const permit = 'permit';
  static const fitnessCert = 'fitness_cert';

  static String label(String t) {
    switch (t) {
      case rc:
        return 'Registration certificate';
      case insurance:
        return 'Insurance';
      case pollutionCert:
        return 'PUC certificate';
      case permit:
        return 'Permit';
      case fitnessCert:
        return 'Fitness certificate';
      default:
        return t;
    }
  }
}

DateTime? _parseTs(dynamic v) {
  if (v == null) return null;
  if (v is String && v.isEmpty) return null;
  try {
    return DateTime.parse(v as String);
  } catch (_) {
    return null;
  }
}

class RiderPartner {
  const RiderPartner({
    required this.id,
    required this.userId,
    required this.partnerType,
    required this.fullName,
    required this.phone,
    this.email,
    this.profilePhotoUrl,
    required this.cityId,
    required this.status,
    required this.kycStatus,
    required this.bankStatus,
    required this.rating,
    required this.totalRidesCompleted,
    required this.totalRidesCancelled,
    required this.acceptanceRate,
    required this.cancellationRate,
    required this.fraudScore,
    required this.isOnline,
    this.lastOnlineAt,
    this.suspendedReason,
    this.blockedReason,
    this.approvedAt,
    required this.createdAt,
  });

  final String id;
  final String userId;
  final PartnerType partnerType;
  final String fullName;
  final String phone;
  final String? email;
  final String? profilePhotoUrl;
  final String cityId;
  final PartnerStatus status;
  final VerificationStatus kycStatus;
  final VerificationStatus bankStatus;
  final double rating;
  final int totalRidesCompleted;
  final int totalRidesCancelled;
  final double acceptanceRate;
  final double cancellationRate;
  final double fraudScore;
  final bool isOnline;
  final DateTime? lastOnlineAt;
  final String? suspendedReason;
  final String? blockedReason;
  final DateTime? approvedAt;
  final DateTime createdAt;

  factory RiderPartner.fromJson(Map<String, dynamic> json) {
    return RiderPartner(
      id: json['id'] as String? ?? '',
      userId: json['user_id'] as String? ?? '',
      partnerType: PartnerType.fromWire(json['partner_type'] as String?),
      fullName: json['full_name'] as String? ?? '',
      phone: json['phone'] as String? ?? '',
      email: json['email'] as String?,
      profilePhotoUrl: json['profile_photo_url'] as String?,
      cityId: json['city_id'] as String? ?? '',
      status: PartnerStatus.fromWire(json['status'] as String?),
      kycStatus: VerificationStatus.fromWire(json['kyc_status'] as String?),
      bankStatus: VerificationStatus.fromWire(json['bank_status'] as String?),
      rating: (json['rating'] as num?)?.toDouble() ?? 0,
      totalRidesCompleted:
          (json['total_rides_completed'] as num?)?.toInt() ?? 0,
      totalRidesCancelled:
          (json['total_rides_cancelled'] as num?)?.toInt() ?? 0,
      acceptanceRate: (json['acceptance_rate'] as num?)?.toDouble() ?? 0,
      cancellationRate: (json['cancellation_rate'] as num?)?.toDouble() ?? 0,
      fraudScore: (json['fraud_score'] as num?)?.toDouble() ?? 0,
      isOnline: json['is_online'] as bool? ?? false,
      lastOnlineAt: _parseTs(json['last_online_at']),
      suspendedReason: json['suspended_reason'] as String?,
      blockedReason: json['blocked_reason'] as String?,
      approvedAt: _parseTs(json['approved_at']),
      createdAt: _parseTs(json['created_at']) ?? DateTime.now(),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'user_id': userId,
        'partner_type': partnerType.wire,
        'full_name': fullName,
        'phone': phone,
        if (email != null) 'email': email,
        if (profilePhotoUrl != null) 'profile_photo_url': profilePhotoUrl,
        'city_id': cityId,
        'status': status.wire,
        'kyc_status': kycStatus.wire,
        'bank_status': bankStatus.wire,
        'rating': rating,
        'total_rides_completed': totalRidesCompleted,
        'total_rides_cancelled': totalRidesCancelled,
        'acceptance_rate': acceptanceRate,
        'cancellation_rate': cancellationRate,
        'fraud_score': fraudScore,
        'is_online': isOnline,
        if (lastOnlineAt != null)
          'last_online_at': lastOnlineAt!.toUtc().toIso8601String(),
        if (suspendedReason != null) 'suspended_reason': suspendedReason,
        if (blockedReason != null) 'blocked_reason': blockedReason,
        if (approvedAt != null)
          'approved_at': approvedAt!.toUtc().toIso8601String(),
        'created_at': createdAt.toUtc().toIso8601String(),
      };
}

class RiderVehicle {
  const RiderVehicle({
    required this.id,
    required this.partnerId,
    required this.vehicleType,
    required this.make,
    required this.model,
    required this.year,
    required this.color,
    required this.registrationNumber,
    required this.status,
    required this.kycStatus,
    required this.createdAt,
  });

  final String id;
  final String partnerId;
  final String vehicleType;
  final String make;
  final String model;
  final int year;
  final String color;
  final String registrationNumber;
  final VerificationStatus status;
  final VerificationStatus kycStatus;
  final DateTime createdAt;

  factory RiderVehicle.fromJson(Map<String, dynamic> json) {
    return RiderVehicle(
      id: json['id'] as String? ?? '',
      partnerId: json['partner_id'] as String? ?? '',
      vehicleType: json['vehicle_type'] as String? ?? '',
      make: json['make'] as String? ?? '',
      model: json['model'] as String? ?? '',
      year: (json['year'] as num?)?.toInt() ?? 0,
      color: json['color'] as String? ?? '',
      registrationNumber: json['registration_number'] as String? ?? '',
      status: VerificationStatus.fromWire(json['status'] as String?),
      kycStatus: VerificationStatus.fromWire(json['kyc_status'] as String?),
      createdAt: _parseTs(json['created_at']) ?? DateTime.now(),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'partner_id': partnerId,
        'vehicle_type': vehicleType,
        'make': make,
        'model': model,
        'year': year,
        'color': color,
        'registration_number': registrationNumber,
        'status': status.wire,
        'kyc_status': kycStatus.wire,
        'created_at': createdAt.toUtc().toIso8601String(),
      };
}

class RiderDocument {
  const RiderDocument({
    required this.id,
    required this.ownerType,
    required this.ownerId,
    required this.documentType,
    this.documentNumber,
    required this.fileUrl,
    required this.status,
    this.rejectionReason,
    this.expiresAt,
    required this.createdAt,
  });

  final String id;

  /// `partner` or `vehicle`.
  final String ownerType;
  final String ownerId;
  final String documentType;
  final String? documentNumber;
  final String fileUrl;
  final VerificationStatus status;
  final String? rejectionReason;
  final DateTime? expiresAt;
  final DateTime createdAt;

  factory RiderDocument.fromJson(Map<String, dynamic> json) {
    return RiderDocument(
      id: json['id'] as String? ?? '',
      ownerType: json['owner_type'] as String? ?? 'partner',
      ownerId: json['owner_id'] as String? ?? '',
      documentType: json['document_type'] as String? ?? '',
      documentNumber: json['document_number'] as String?,
      fileUrl: json['file_url'] as String? ?? '',
      status: VerificationStatus.fromWire(json['status'] as String?),
      rejectionReason: json['rejection_reason'] as String?,
      expiresAt: _parseTs(json['expires_at']),
      createdAt: _parseTs(json['created_at']) ?? DateTime.now(),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'owner_type': ownerType,
        'owner_id': ownerId,
        'document_type': documentType,
        if (documentNumber != null) 'document_number': documentNumber,
        'file_url': fileUrl,
        'status': status.wire,
        if (rejectionReason != null) 'rejection_reason': rejectionReason,
        if (expiresAt != null)
          'expires_at': expiresAt!.toUtc().toIso8601String(),
        'created_at': createdAt.toUtc().toIso8601String(),
      };
}

class SubscriptionPlan {
  const SubscriptionPlan({
    required this.id,
    required this.name,
    required this.priceInrPaise,
    required this.durationDays,
    required this.leadAllotment,
    required this.planPriorityWeight,
    required this.isUnlimited,
    required this.isFairUse,
    required this.isActive,
  });

  final String id;
  final String name;
  final int priceInrPaise;
  final int durationDays;
  final int leadAllotment;
  final double planPriorityWeight;
  final bool isUnlimited;
  final bool isFairUse;
  final bool isActive;

  bool get isTrial =>
      priceInrPaise == 0 || name.toLowerCase().contains('trial');

  factory SubscriptionPlan.fromJson(Map<String, dynamic> json) {
    return SubscriptionPlan(
      id: json['id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      priceInrPaise: (json['price_inr_paise'] as num?)?.toInt() ?? 0,
      durationDays: (json['duration_days'] as num?)?.toInt() ?? 30,
      leadAllotment: (json['lead_allotment'] as num?)?.toInt() ?? 0,
      planPriorityWeight:
          (json['plan_priority_weight'] as num?)?.toDouble() ?? 1.0,
      isUnlimited: json['is_unlimited'] as bool? ?? false,
      isFairUse: json['is_fair_use'] as bool? ?? false,
      isActive: json['is_active'] as bool? ?? true,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'price_inr_paise': priceInrPaise,
        'duration_days': durationDays,
        'lead_allotment': leadAllotment,
        'plan_priority_weight': planPriorityWeight,
        'is_unlimited': isUnlimited,
        'is_fair_use': isFairUse,
        'is_active': isActive,
      };
}

class PartnerSubscription {
  const PartnerSubscription({
    required this.id,
    required this.partnerId,
    required this.planId,
    required this.planName,
    required this.status,
    required this.leadsUsed,
    required this.leadAllotment,
    required this.startedAt,
    required this.expiresAt,
    this.graceEndsAt,
    required this.autoRenew,
    this.cancelledAt,
    this.renewalFailureCount = 0,
  });

  final String id;
  final String partnerId;
  final String planId;
  final String planName;
  final SubscriptionStatus status;
  final int leadsUsed;
  final int leadAllotment;
  final DateTime startedAt;
  final DateTime expiresAt;
  final DateTime? graceEndsAt;
  final bool autoRenew;
  final DateTime? cancelledAt;

  /// Sprint 4 — backend cron tracks how many times the auto-renew attempt
  /// has failed for the *current* cycle. >=1 surfaces an amber banner so
  /// the partner tops up wallet before the next retry.
  final int renewalFailureCount;

  int get leadsRemaining {
    final r = leadAllotment - leadsUsed;
    return r < 0 ? 0 : r;
  }

  int get daysRemaining {
    final d = expiresAt.difference(DateTime.now()).inDays;
    return d < 0 ? 0 : d;
  }

  /// Lead-usage progress in [0.0, 1.0]. 0 when allotment is unknown.
  double get leadUsageRatio {
    if (leadAllotment <= 0) return 0;
    final r = leadsUsed / leadAllotment;
    if (r < 0) return 0;
    if (r > 1) return 1;
    return r;
  }

  factory PartnerSubscription.fromJson(Map<String, dynamic> json) {
    return PartnerSubscription(
      id: json['id'] as String? ?? '',
      partnerId: json['partner_id'] as String? ?? '',
      planId: json['plan_id'] as String? ?? '',
      planName: json['plan_name'] as String? ?? '',
      status: SubscriptionStatus.fromWire(json['status'] as String?),
      leadsUsed: (json['leads_used'] as num?)?.toInt() ?? 0,
      leadAllotment: (json['lead_allotment'] as num?)?.toInt() ?? 0,
      startedAt: _parseTs(json['started_at']) ?? DateTime.now(),
      expiresAt: _parseTs(json['expires_at']) ?? DateTime.now(),
      graceEndsAt: _parseTs(json['grace_ends_at']),
      autoRenew: json['auto_renew'] as bool? ?? false,
      cancelledAt: _parseTs(json['cancelled_at']),
      renewalFailureCount:
          (json['renewal_failure_count'] as num?)?.toInt() ?? 0,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'partner_id': partnerId,
        'plan_id': planId,
        'plan_name': planName,
        'status': status.wire,
        'leads_used': leadsUsed,
        'lead_allotment': leadAllotment,
        'started_at': startedAt.toUtc().toIso8601String(),
        'expires_at': expiresAt.toUtc().toIso8601String(),
        if (graceEndsAt != null)
          'grace_ends_at': graceEndsAt!.toUtc().toIso8601String(),
        'auto_renew': autoRenew,
        if (cancelledAt != null)
          'cancelled_at': cancelledAt!.toUtc().toIso8601String(),
        'renewal_failure_count': renewalFailureCount,
      };
}

/// Sprint 4 — partner referral programme. v1 backend doesn't yet expose a
/// dedicated endpoint, so the providers layer falls back to a stub built
/// from `partnerId`. The model shape mirrors the eventual S5 backend
/// response so we won't have to thread changes through the UI later.
class ReferralStats {
  const ReferralStats({
    required this.code,
    required this.pendingCount,
    required this.activatedCount,
    required this.totalRewardLeads,
    this.shareUrl,
  });

  final String code;
  final int pendingCount;
  final int activatedCount;
  final int totalRewardLeads;
  final String? shareUrl;

  factory ReferralStats.fromJson(Map<String, dynamic> json) {
    return ReferralStats(
      code: json['code'] as String? ?? '',
      pendingCount: (json['pending_count'] as num?)?.toInt() ?? 0,
      activatedCount: (json['activated_count'] as num?)?.toInt() ?? 0,
      totalRewardLeads:
          (json['total_reward_leads'] as num?)?.toInt() ?? 0,
      shareUrl: json['share_url'] as String?,
    );
  }

  Map<String, dynamic> toJson() => {
        'code': code,
        'pending_count': pendingCount,
        'activated_count': activatedCount,
        'total_reward_leads': totalRewardLeads,
        if (shareUrl != null) 'share_url': shareUrl,
      };
}

class SubscriptionPayment {
  const SubscriptionPayment({
    required this.id,
    required this.subscriptionId,
    required this.planId,
    required this.amountPaise,
    required this.paymentMethod,
    required this.status,
    this.walletTxnId,
    this.upiTxnRef,
    this.paymentProofUrl,
    this.upiIntentUrl,
    required this.createdAt,
    this.verifiedAt,
  });

  final String id;
  final String subscriptionId;
  final String planId;
  final int amountPaise;
  final String paymentMethod;
  final String status;
  final String? walletTxnId;
  final String? upiTxnRef;
  final String? paymentProofUrl;

  /// Optional UPI Intent URL returned when `paymentMethod == 'upi'`.
  /// Surfaced to the partner via `launchUPIIntent`.
  final String? upiIntentUrl;
  final DateTime createdAt;
  final DateTime? verifiedAt;

  factory SubscriptionPayment.fromJson(Map<String, dynamic> json) {
    return SubscriptionPayment(
      id: json['id'] as String? ?? '',
      subscriptionId: json['subscription_id'] as String? ?? '',
      planId: json['plan_id'] as String? ?? '',
      amountPaise: (json['amount_paise'] as num?)?.toInt() ?? 0,
      paymentMethod: json['payment_method'] as String? ?? '',
      status: json['status'] as String? ?? 'pending',
      walletTxnId: json['wallet_txn_id'] as String?,
      upiTxnRef: json['upi_txn_ref'] as String?,
      paymentProofUrl: json['payment_proof_url'] as String?,
      upiIntentUrl: json['upi_intent_url'] as String?,
      createdAt: _parseTs(json['created_at']) ?? DateTime.now(),
      verifiedAt: _parseTs(json['verified_at']),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'subscription_id': subscriptionId,
        'plan_id': planId,
        'amount_paise': amountPaise,
        'payment_method': paymentMethod,
        'status': status,
        if (walletTxnId != null) 'wallet_txn_id': walletTxnId,
        if (upiTxnRef != null) 'upi_txn_ref': upiTxnRef,
        if (paymentProofUrl != null) 'payment_proof_url': paymentProofUrl,
        if (upiIntentUrl != null) 'upi_intent_url': upiIntentUrl,
        'created_at': createdAt.toUtc().toIso8601String(),
        if (verifiedAt != null)
          'verified_at': verifiedAt!.toUtc().toIso8601String(),
      };
}

class RideOffer {
  const RideOffer({
    required this.id,
    required this.rideId,
    required this.partnerId,
    required this.score,
    required this.distanceKm,
    required this.expiresAt,
    required this.status,
    required this.vehicleType,
    required this.pickupAddress,
    required this.dropAddress,
    required this.fareEstimatePaise,
    required this.etaToPickupSeconds,
    this.customerName,
    this.customerRating,
  });

  final String id;
  final String rideId;
  final String partnerId;
  final double score;
  final double distanceKm;
  final DateTime expiresAt;

  /// Wire string: `pending | accepted | rejected | expired | cancelled`.
  final String status;
  final String vehicleType;

  /// Pickup display string. PRIVACY: city/area only — never the full
  /// street address. Backend already redacts before sending.
  final String pickupAddress;
  final String dropAddress;
  final int fareEstimatePaise;
  final int etaToPickupSeconds;
  final String? customerName;
  final double? customerRating;

  Duration get timeRemaining {
    final d = expiresAt.difference(DateTime.now());
    return d.isNegative ? Duration.zero : d;
  }

  factory RideOffer.fromJson(Map<String, dynamic> json) {
    return RideOffer(
      id: json['id'] as String? ?? '',
      rideId: json['ride_id'] as String? ?? '',
      partnerId: json['partner_id'] as String? ?? '',
      score: (json['score'] as num?)?.toDouble() ?? 0,
      distanceKm: (json['distance_km'] as num?)?.toDouble() ?? 0,
      expiresAt: _parseTs(json['expires_at']) ??
          DateTime.now().add(const Duration(seconds: 15)),
      status: json['status'] as String? ?? 'pending',
      vehicleType: json['vehicle_type'] as String? ?? '',
      pickupAddress: json['pickup_address'] as String? ?? '',
      dropAddress: json['drop_address'] as String? ?? '',
      fareEstimatePaise: (json['fare_estimate_paise'] as num?)?.toInt() ?? 0,
      etaToPickupSeconds:
          (json['eta_to_pickup_seconds'] as num?)?.toInt() ?? 0,
      customerName: json['customer_name'] as String?,
      customerRating: (json['customer_rating'] as num?)?.toDouble(),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'ride_id': rideId,
        'partner_id': partnerId,
        'score': score,
        'distance_km': distanceKm,
        'expires_at': expiresAt.toUtc().toIso8601String(),
        'status': status,
        'vehicle_type': vehicleType,
        'pickup_address': pickupAddress,
        'drop_address': dropAddress,
        'fare_estimate_paise': fareEstimatePaise,
        'eta_to_pickup_seconds': etaToPickupSeconds,
        if (customerName != null) 'customer_name': customerName,
        if (customerRating != null) 'customer_rating': customerRating,
      };
}

/// Reason buckets the partner picks when rejecting an offer. Strings (not
/// enum) so the backend can add new reasons without breaking the client.
class RideRejectReason {
  RideRejectReason._();

  static const tooFar = 'too_far';
  static const wrongDirection = 'wrong_direction';
  static const fareTooLow = 'fare_too_low';
  static const breakTime = 'break_time';
  static const vehicleIssue = 'vehicle_issue';
  static const other = 'other';

  static const all = <String>[
    tooFar,
    wrongDirection,
    fareTooLow,
    breakTime,
    vehicleIssue,
    other,
  ];

  static String label(String r) {
    switch (r) {
      case tooFar:
        return 'Too far away';
      case wrongDirection:
        return 'Wrong direction';
      case fareTooLow:
        return 'Fare too low';
      case breakTime:
        return 'Taking a break';
      case vehicleIssue:
        return 'Vehicle issue';
      case other:
        return 'Other';
      default:
        return r;
    }
  }
}

class PartnerDashboard {
  const PartnerDashboard({
    required this.partnerId,
    required this.planName,
    required this.planStatus,
    required this.leadsUsed,
    required this.leadAllotment,
    required this.rating,
    required this.acceptanceRatePct,
    required this.cancellationRatePct,
    required this.completedRidesToday,
    required this.todayEarningsPaise,
    required this.payoutStatus,
    this.planExpiresAt,
  });

  final String partnerId;
  final String planName;
  final SubscriptionStatus planStatus;
  final int leadsUsed;
  final int leadAllotment;
  final double rating;
  final double acceptanceRatePct;
  final double cancellationRatePct;
  final int completedRidesToday;
  final int todayEarningsPaise;
  final String payoutStatus;
  final DateTime? planExpiresAt;

  factory PartnerDashboard.fromJson(Map<String, dynamic> json) {
    return PartnerDashboard(
      partnerId: json['partner_id'] as String? ?? '',
      planName: json['plan_name'] as String? ?? '',
      planStatus: SubscriptionStatus.fromWire(json['plan_status'] as String?),
      leadsUsed: (json['leads_used'] as num?)?.toInt() ?? 0,
      leadAllotment: (json['lead_allotment'] as num?)?.toInt() ?? 0,
      rating: (json['rating'] as num?)?.toDouble() ?? 0,
      acceptanceRatePct:
          (json['acceptance_rate_pct'] as num?)?.toDouble() ?? 0,
      cancellationRatePct:
          (json['cancellation_rate_pct'] as num?)?.toDouble() ?? 0,
      completedRidesToday:
          (json['completed_rides_today'] as num?)?.toInt() ?? 0,
      todayEarningsPaise:
          (json['today_earnings_paise'] as num?)?.toInt() ?? 0,
      payoutStatus: json['payout_status'] as String? ?? 'idle',
      planExpiresAt: _parseTs(json['plan_expires_at']),
    );
  }

  Map<String, dynamic> toJson() => {
        'partner_id': partnerId,
        'plan_name': planName,
        'plan_status': planStatus.wire,
        'leads_used': leadsUsed,
        'lead_allotment': leadAllotment,
        'rating': rating,
        'acceptance_rate_pct': acceptanceRatePct,
        'cancellation_rate_pct': cancellationRatePct,
        'completed_rides_today': completedRidesToday,
        'today_earnings_paise': todayEarningsPaise,
        'payout_status': payoutStatus,
        if (planExpiresAt != null)
          'plan_expires_at': planExpiresAt!.toUtc().toIso8601String(),
      };
}

class EarningsBreakdownItem {
  const EarningsBreakdownItem({
    required this.date,
    required this.ridesCount,
    required this.earningsPaise,
  });

  final DateTime date;
  final int ridesCount;
  final int earningsPaise;

  factory EarningsBreakdownItem.fromJson(Map<String, dynamic> json) {
    return EarningsBreakdownItem(
      date: _parseTs(json['date']) ?? DateTime.now(),
      ridesCount: (json['rides_count'] as num?)?.toInt() ?? 0,
      earningsPaise: (json['earnings_paise'] as num?)?.toInt() ?? 0,
    );
  }

  Map<String, dynamic> toJson() => {
        'date': date.toUtc().toIso8601String(),
        'rides_count': ridesCount,
        'earnings_paise': earningsPaise,
      };
}

class EarningsSnapshot {
  const EarningsSnapshot({
    required this.period,
    required this.totalEarningsPaise,
    required this.completedRides,
    required this.avgFarePaise,
    required this.breakdown,
  });

  /// `today` | `week` | `month`.
  final String period;
  final int totalEarningsPaise;
  final int completedRides;
  final int avgFarePaise;
  final List<EarningsBreakdownItem> breakdown;

  factory EarningsSnapshot.fromJson(Map<String, dynamic> json) {
    final raw = json['breakdown'];
    final list = (raw is List) ? raw : const <dynamic>[];
    return EarningsSnapshot(
      period: json['period'] as String? ?? 'today',
      totalEarningsPaise:
          (json['total_earnings_paise'] as num?)?.toInt() ?? 0,
      completedRides: (json['completed_rides'] as num?)?.toInt() ?? 0,
      avgFarePaise: (json['avg_fare_paise'] as num?)?.toInt() ?? 0,
      breakdown: list
          .whereType<Map>()
          .map((e) => EarningsBreakdownItem.fromJson(e.cast<String, dynamic>()))
          .toList(),
    );
  }

  Map<String, dynamic> toJson() => {
        'period': period,
        'total_earnings_paise': totalEarningsPaise,
        'completed_rides': completedRides,
        'avg_fare_paise': avgFarePaise,
        'breakdown': breakdown.map((e) => e.toJson()).toList(),
      };
}

// ════════════════════════════════════════════════════════════════════════
// Sprint 3 — Customer-side safety models.
//
// Spec: `mopedu/MOPEDU_SPEC.md` §12 (safety features).
// Backend (Sprint 3):
//   POST /v1/rider/rides/:id/sos
//   POST /v1/rider/rides/:id/share
//   GET  /v1/rider/share/:token              (public, no auth)
//   POST /v1/rider/rides/:id/complain
//   GET  /v1/rider/complaints/me
//   GET  /v1/rider/trusted-contact
//   PUT  /v1/rider/trusted-contact
//
// PRIVACY: phone is NEVER passed to telemetry. The `maskedPhone` helper
// here is for display only.
// ════════════════════════════════════════════════════════════════════════

/// Trusted contact = a phone number the customer wants notified during
/// SOS. `shareLocationOnSos` controls whether a share-ride link is also
/// pushed to the contact when SOS fires.
class TrustedContact {
  const TrustedContact({
    required this.name,
    required this.phone,
    this.relationship,
    this.shareLocationOnSos = true,
  });

  final String name;
  final String phone;
  final String? relationship;
  final bool shareLocationOnSos;

  /// Display-only masked phone. NEVER pass the raw `phone` to telemetry.
  String get maskedPhone {
    final p = phone.replaceAll(RegExp(r'\s+'), '');
    if (p.length < 4) return '****';
    final tail = p.substring(p.length - 4);
    return '${'*' * (p.length - 4)}$tail';
  }

  factory TrustedContact.fromJson(Map<String, dynamic> json) {
    return TrustedContact(
      name: json['name'] as String? ?? '',
      phone: json['phone'] as String? ?? '',
      relationship: json['relationship'] as String?,
      shareLocationOnSos: json['share_on_sos'] as bool? ??
          json['share_location_on_sos'] as bool? ??
          true,
    );
  }

  Map<String, dynamic> toJson() => {
        'name': name,
        'phone': phone,
        if (relationship != null && relationship!.isNotEmpty)
          'relationship': relationship,
        'share_on_sos': shareLocationOnSos,
      };

  TrustedContact copyWith({
    String? name,
    String? phone,
    String? relationship,
    bool? shareLocationOnSos,
  }) {
    return TrustedContact(
      name: name ?? this.name,
      phone: phone ?? this.phone,
      relationship: relationship ?? this.relationship,
      shareLocationOnSos: shareLocationOnSos ?? this.shareLocationOnSos,
    );
  }
}

/// Categorical buckets the customer picks when filing a complaint.
/// Backend uses snake_case wire strings.
enum ComplaintCategory {
  driverBehavior,
  vehicleCondition,
  routeDeviation,
  fareDispute,
  safety,
  other;

  String toJson() => wire;

  String get wire {
    switch (this) {
      case ComplaintCategory.driverBehavior:
        return 'driver_behavior';
      case ComplaintCategory.vehicleCondition:
        return 'vehicle_condition';
      case ComplaintCategory.routeDeviation:
        return 'route_deviation';
      case ComplaintCategory.fareDispute:
        return 'fare_dispute';
      case ComplaintCategory.safety:
        return 'safety';
      case ComplaintCategory.other:
        return 'other';
    }
  }

  String get label {
    switch (this) {
      case ComplaintCategory.driverBehavior:
        return 'Driver behaviour';
      case ComplaintCategory.vehicleCondition:
        return 'Vehicle condition';
      case ComplaintCategory.routeDeviation:
        return 'Route deviation';
      case ComplaintCategory.fareDispute:
        return 'Fare dispute';
      case ComplaintCategory.safety:
        return 'Safety';
      case ComplaintCategory.other:
        return 'Other';
    }
  }

  static ComplaintCategory fromWire(String? s) {
    switch (s) {
      case 'driver_behavior':
        return ComplaintCategory.driverBehavior;
      case 'vehicle_condition':
        return ComplaintCategory.vehicleCondition;
      case 'route_deviation':
        return ComplaintCategory.routeDeviation;
      case 'fare_dispute':
        return ComplaintCategory.fareDispute;
      case 'safety':
        return ComplaintCategory.safety;
      case 'other':
      default:
        return ComplaintCategory.other;
    }
  }
}

/// Status strings from the complaint queue. Strings (not enum) so the
/// backend can extend without breaking older clients.
class ComplaintStatus {
  ComplaintStatus._();

  static const open = 'open';
  static const inReview = 'in_review';
  static const resolved = 'resolved';
  static const closed = 'closed';

  static String label(String s) {
    switch (s) {
      case open:
        return 'Open';
      case inReview:
        return 'In review';
      case resolved:
        return 'Resolved';
      case closed:
        return 'Closed';
      default:
        return s;
    }
  }

  static bool isTerminal(String s) => s == resolved || s == closed;
}

class Complaint {
  const Complaint({
    required this.id,
    required this.rideId,
    required this.category,
    this.description,
    required this.status,
    this.resolutionNote,
    required this.createdAt,
    this.resolvedAt,
  });

  final String id;
  final String rideId;
  final ComplaintCategory category;
  final String? description;
  final String status;
  final String? resolutionNote;
  final DateTime createdAt;
  final DateTime? resolvedAt;

  factory Complaint.fromJson(Map<String, dynamic> json) {
    return Complaint(
      id: json['id'] as String? ?? '',
      rideId: json['ride_id'] as String? ?? '',
      category: ComplaintCategory.fromWire(json['category'] as String?),
      description: json['description'] as String?,
      status: json['status'] as String? ?? ComplaintStatus.open,
      resolutionNote: json['resolution_note'] as String?,
      createdAt: _parseTs(json['created_at']) ?? DateTime.now(),
      resolvedAt: _parseTs(json['resolved_at']),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'ride_id': rideId,
        'category': category.wire,
        if (description != null) 'description': description,
        'status': status,
        if (resolutionNote != null) 'resolution_note': resolutionNote,
        'created_at': createdAt.toUtc().toIso8601String(),
        if (resolvedAt != null)
          'resolved_at': resolvedAt!.toUtc().toIso8601String(),
      };
}

/// Public, redacted ride view served by `GET /v1/rider/share/:token`.
/// First-name only, no phone, no full address — the backend strips
/// everything else before sending.
class SharedRideView {
  const SharedRideView({
    required this.rideId,
    required this.status,
    required this.dropArea,
    required this.partnerFirstName,
    this.partnerPhotoUrl,
    required this.vehicleNumber,
    required this.etaSeconds,
    this.currentLocation,
  });

  final String rideId;
  final String status;
  final String dropArea;
  final String partnerFirstName;
  final String? partnerPhotoUrl;
  final String vehicleNumber;
  final int etaSeconds;
  final RidePoint? currentLocation;

  bool get isTerminal => RideStatus.isTerminal(status);

  factory SharedRideView.fromJson(Map<String, dynamic> json) {
    final loc = json['current_location'];
    return SharedRideView(
      rideId: json['ride_id'] as String? ?? '',
      status: json['status'] as String? ?? RideStatus.requested,
      dropArea: json['drop_area'] as String? ?? '',
      partnerFirstName: json['partner_first_name'] as String? ?? 'Driver',
      partnerPhotoUrl: json['partner_photo_url'] as String?,
      vehicleNumber: json['vehicle_number'] as String? ?? '',
      etaSeconds: (json['eta_seconds'] as num?)?.toInt() ?? 0,
      currentLocation: loc is Map<String, dynamic>
          ? RidePoint.fromJson(loc)
          : null,
    );
  }

  Map<String, dynamic> toJson() => {
        'ride_id': rideId,
        'status': status,
        'drop_area': dropArea,
        'partner_first_name': partnerFirstName,
        if (partnerPhotoUrl != null) 'partner_photo_url': partnerPhotoUrl,
        'vehicle_number': vehicleNumber,
        'eta_seconds': etaSeconds,
        if (currentLocation != null)
          'current_location': currentLocation!.toJson(),
      };
}

/// Result of `POST /v1/rider/rides/:id/share`. The customer copies/sends
/// `shareUrl`; `expiresAt` drives the "valid until ride ends" copy.
class ShareTokenResult {
  const ShareTokenResult({
    required this.token,
    required this.expiresAt,
    required this.shareUrl,
  });

  final String token;
  final DateTime expiresAt;
  final String shareUrl;

  factory ShareTokenResult.fromJson(Map<String, dynamic> json) {
    return ShareTokenResult(
      token: json['token'] as String? ?? '',
      expiresAt: _parseTs(json['expires_at']) ??
          DateTime.now().add(const Duration(hours: 6)),
      shareUrl: json['share_url'] as String? ?? '',
    );
  }

  Map<String, dynamic> toJson() => {
        'token': token,
        'expires_at': expiresAt.toUtc().toIso8601String(),
        'share_url': shareUrl,
      };
}

/// Aadhaar (DigiLocker) flow start. Mirrors `AadhaarFlowStart` from the
/// Pulse package but lives here so the partner onboarding screen doesn't
/// import Pulse internals.
class PartnerAadhaarFlowStart {
  const PartnerAadhaarFlowStart({
    required this.authorizeUrl,
    required this.state,
  });

  final String authorizeUrl;
  final String state;

  factory PartnerAadhaarFlowStart.fromJson(Map<String, dynamic> json) {
    return PartnerAadhaarFlowStart(
      authorizeUrl: json['authorize_url'] as String? ?? '',
      state: json['state'] as String? ?? '',
    );
  }
}
