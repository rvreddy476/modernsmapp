// FiGo rewards repository — wraps the customer-facing loyalty +
// referral endpoints shipped in food-service Wave G4.4 + G4.6.
//
//   GET  /v1/food/me/loyalty
//   POST /v1/food/me/loyalty/redeem
//   GET  /v1/food/me/referral
//   POST /v1/food/me/referral/apply
//
// Endpoint contracts in food-service/internal/http/handler_rewards.go.

import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class FoodLoyaltyBalance {
  const FoodLoyaltyBalance({
    required this.userId,
    required this.pointsBalance,
    required this.lifetimeEarned,
    required this.tier,
  });

  final String userId;
  final int pointsBalance;
  final int lifetimeEarned;
  final String tier;

  factory FoodLoyaltyBalance.fromJson(Map<String, dynamic> json) {
    return FoodLoyaltyBalance(
      userId: (json['user_id'] as String?) ?? '',
      pointsBalance: (json['points_balance'] as num?)?.toInt() ?? 0,
      lifetimeEarned: (json['lifetime_earned'] as num?)?.toInt() ?? 0,
      tier: (json['tier'] as String?) ?? 'bronze',
    );
  }
}

class FoodLoyaltyLedgerRow {
  const FoodLoyaltyLedgerRow({
    required this.id,
    required this.delta,
    required this.reason,
    required this.createdAt,
    this.orderId,
  });

  final String id;
  final int delta;
  final String reason;
  final DateTime? createdAt;
  final String? orderId;

  factory FoodLoyaltyLedgerRow.fromJson(Map<String, dynamic> json) {
    DateTime? parseTs(dynamic v) {
      if (v is String && v.isNotEmpty) {
        return DateTime.tryParse(v);
      }
      return null;
    }

    return FoodLoyaltyLedgerRow(
      id: (json['id'] as String?) ?? '',
      delta: (json['delta'] as num?)?.toInt() ?? 0,
      reason: (json['reason'] as String?) ?? '',
      createdAt: parseTs(json['created_at']),
      orderId: json['order_id'] as String?,
    );
  }
}

class FoodLoyaltySnapshot {
  const FoodLoyaltySnapshot({
    required this.balance,
    required this.ledger,
  });

  final FoodLoyaltyBalance balance;
  final List<FoodLoyaltyLedgerRow> ledger;
}

class FoodRewardsRepository {
  FoodRewardsRepository(this._api);

  final ApiClient _api;

  // ─── Loyalty ────────────────────────────────────────────────────────

  /// `GET /v1/food/me/loyalty`
  Future<FoodLoyaltySnapshot> getLoyalty() async {
    final res = await _api.get('/v1/food/me/loyalty');
    final data = _unwrap(res.data);
    final balance = FoodLoyaltyBalance.fromJson(
      Map<String, dynamic>.from(data['balance'] as Map? ?? const {}),
    );
    final ledger = (data['ledger'] as List? ?? const [])
        .whereType<Map>()
        .map((m) => FoodLoyaltyLedgerRow.fromJson(
              Map<String, dynamic>.from(m),
            ))
        .toList(growable: false);
    return FoodLoyaltySnapshot(balance: balance, ledger: ledger);
  }

  /// `POST /v1/food/me/loyalty/redeem`
  Future<FoodLoyaltyBalance> redeemLoyalty({
    required int points,
    String? orderId,
  }) async {
    final res = await _api.post(
      '/v1/food/me/loyalty/redeem',
      data: <String, dynamic>{
        'points': points,
        'order_id': ?orderId,
      },
    );
    return FoodLoyaltyBalance.fromJson(
      Map<String, dynamic>.from(_unwrap(res.data) as Map),
    );
  }

  // ─── Referrals ──────────────────────────────────────────────────────

  /// `GET /v1/food/me/referral`
  Future<String> getReferralCode() async {
    final res = await _api.get('/v1/food/me/referral');
    final data = _unwrap(res.data);
    return (data['code'] as String?) ?? '';
  }

  /// `POST /v1/food/me/referral/apply`
  Future<void> applyReferralCode(String code) async {
    await _api.post(
      '/v1/food/me/referral/apply',
      data: <String, dynamic>{'code': code},
    );
  }
}

/// Unwraps `{ data, error, meta }` envelope.
dynamic _unwrap(dynamic body) {
  if (body is Map && body['data'] != null) return body['data'];
  return body;
}

final figoRewardsRepositoryProvider = Provider<FoodRewardsRepository>((ref) {
  return FoodRewardsRepository(ref.watch(apiClientProvider));
});
