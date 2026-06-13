import 'package:atpost_app/data/models/monetization.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// One creator-membership tier — what the fan picks from when joining.
class CreatorTier {
  final String id;
  final String creatorId;
  final String name;
  final int pricePaise;
  final String currency;
  final List<String> perks;
  final int subscriberCount;
  final bool isActive;

  const CreatorTier({
    required this.id,
    required this.creatorId,
    required this.name,
    required this.pricePaise,
    required this.currency,
    required this.perks,
    required this.subscriberCount,
    required this.isActive,
  });

  factory CreatorTier.fromJson(Map<String, dynamic> json) {
    final pricePaise = (json['price_paise'] is num)
        ? (json['price_paise'] as num).toInt()
        : 0;
    final perksRaw = json['perks'];
    final perks = <String>[];
    if (perksRaw is List) {
      for (final p in perksRaw) {
        if (p is String) perks.add(p);
      }
    }
    return CreatorTier(
      id: (json['id'] ?? '').toString(),
      creatorId: (json['creator_id'] ?? '').toString(),
      name: (json['name'] ?? '').toString(),
      pricePaise: pricePaise,
      currency: (json['currency'] ?? 'INR').toString(),
      perks: perks,
      subscriberCount: (json['subscriber_count'] is num)
          ? (json['subscriber_count'] as num).toInt()
          : 0,
      isActive: json['is_active'] == true,
    );
  }

  double get priceRupees => pricePaise / 100.0;
}

/// Result of GET /v1/monetization/entitlements?creator_id=X[&tier_id=Y]
class Entitlement {
  final bool allowed;
  final String? activeTierId;
  final int activePricePaise;
  final String? requiredTierId;
  final int requiredPricePaise;
  final String? reason;

  const Entitlement({
    required this.allowed,
    this.activeTierId,
    required this.activePricePaise,
    this.requiredTierId,
    required this.requiredPricePaise,
    this.reason,
  });

  factory Entitlement.fromJson(Map<String, dynamic> json) {
    return Entitlement(
      allowed: json['allowed'] == true,
      activeTierId: json['active_tier_id']?.toString(),
      activePricePaise: (json['active_price_paise'] is num)
          ? (json['active_price_paise'] as num).toInt()
          : 0,
      requiredTierId: json['required_tier_id']?.toString(),
      requiredPricePaise: (json['required_price_paise'] is num)
          ? (json['required_price_paise'] as num).toInt()
          : 0,
      reason: json['reason']?.toString(),
    );
  }
}

/// Production-ready repository for monetization operations.
class MonetizationRepository {
  final ApiClient _api;

  MonetizationRepository(this._api);

  /// Fetch earnings summary for the current user (analytics-side data).
  Future<EarningsSummary> getEarningsSummary() async {
    final response = await _api.get('/v1/analytics/creator/me');
    return EarningsSummary.fromJson(response.data as Map<String, dynamic>);
  }

  /// Phase F1.1 — re-pointed from the retired `/v1/shop/payouts` to the
  /// commerce-service per-line earnings ledger added in Phase 4.4. The
  /// response shape changed: each row is now an order item with gross
  /// / commission / fee / TDS / net broken out instead of a single
  /// aggregated payout transaction.
  Future<List<SellerEarning>> getSellerEarnings({int limit = 50, int offset = 0}) async {
    final response = await _api.get(
      '/v1/commerce/seller/earnings',
      queryParameters: {'limit': limit, 'offset': offset},
    );
    final data = response.data['data'] as Map<String, dynamic>? ?? const {};
    final items = (data['earnings'] as List<dynamic>?) ?? [];
    return items
        .map((e) => SellerEarning.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Legacy-shape adapter so the monetization dashboard (which still
  /// renders PayoutRecord rows) keeps working without a UI rewrite.
  /// Each commerce earning row collapses into a PayoutRecord with the
  /// net amount + delivered_at + payment_method. Will be removed once
  /// the dashboard migrates to the richer SellerEarning view.
  Future<List<PayoutRecord>> getPayouts() async {
    final earnings = await getSellerEarnings(limit: 100);
    return earnings
        .map((e) => PayoutRecord(
              id: e.orderItemId,
              amount: e.netAmount,
              status: e.status.isEmpty ? 'pending' : e.status,
              createdAt: e.deliveredAt ?? DateTime.now(),
              method: e.paymentMethod,
            ))
        .toList();
  }

  /// Earnings history for chart data.
  Future<List<Map<String, dynamic>>> getEarningsHistory({
    String period = '30d',
  }) async {
    final response = await _api.get(
      '/v1/analytics/creator/me',
      queryParameters: {'period': period},
    );
    final history = (response.data['history'] as List<dynamic>?) ?? [];
    return history
        .map((e) => Map<String, dynamic>.from(e as Map))
        .toList();
  }

  // ---------------------------------------------------------------------
  // Tier 3c — Memberships
  // ---------------------------------------------------------------------

  /// Public: list a creator's active tiers (for the fan-side tier
  /// picker on a profile page).
  Future<List<CreatorTier>> getCreatorTiers(String creatorId) async {
    final res = await _api.get('/v1/monetization/creators/$creatorId/tiers');
    final list = (res.data['data'] as List<dynamic>?) ?? [];
    return list
        .map((e) => CreatorTier.fromJson(Map<String, dynamic>.from(e as Map)))
        .toList();
  }

  /// Subscribe the caller to a tier. Throws on failure (insufficient
  /// balance, already subscribed, etc).
  Future<void> subscribe({
    required String creatorId,
    required String tierId,
  }) async {
    await _api.post(
      '/v1/monetization/subscribe/$creatorId',
      data: {'tier_id': tierId},
    );
  }

  /// Cancel the caller's active subscription to a creator.
  Future<void> unsubscribe(String creatorId) async {
    await _api.delete('/v1/monetization/subscribe/$creatorId');
  }

  /// Check whether the caller is entitled to a creator's content
  /// (optionally at a specific tier). The dashboard / paywall use
  /// this for "you're a member" vs "subscribe to view" UI.
  Future<Entitlement> checkEntitlement({
    required String creatorId,
    String? tierId,
  }) async {
    final res = await _api.get(
      '/v1/monetization/entitlements',
      queryParameters: {
        'creator_id': creatorId,
        'tier_id': ?tierId,
      },
    );
    return Entitlement.fromJson(
      Map<String, dynamic>.from(res.data['data'] as Map),
    );
  }

  // ---------------------------------------------------------------------
  // Tier 3a — Creator Fund
  // ---------------------------------------------------------------------

  Future<Map<String, dynamic>> getCreatorFundStatus() async {
    final res = await _api.get('/v1/monetization/creator-fund/status');
    return Map<String, dynamic>.from(res.data['data'] as Map);
  }

  Future<Map<String, dynamic>> applyCreatorFund() async {
    final res = await _api.post('/v1/monetization/creator-fund/apply');
    return Map<String, dynamic>.from(res.data['data'] as Map? ?? {});
  }

  Future<Map<String, dynamic>> getCreatorFundEarnings({int days = 30}) async {
    final res = await _api.get(
      '/v1/monetization/creator-fund/earnings',
      queryParameters: {'days': days},
    );
    return Map<String, dynamic>.from(res.data['data'] as Map);
  }
}

final monetizationRepositoryProvider = Provider<MonetizationRepository>((ref) {
  return MonetizationRepository(ref.watch(apiClientProvider));
});
