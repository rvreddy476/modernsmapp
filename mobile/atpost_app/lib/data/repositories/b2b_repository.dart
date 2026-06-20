// Phase F4 mobile — B2B repository.
//
// Wraps the Phase 5 / F2 commerce-service endpoints the buyer flow
// touches: list-my-orgs, fetch-tier-ladder, RFQ create / list / detail
// / accept / reject. Sellers' RFQ inbox lives on web — mobile is buyer
// surfaces only per the long-standing "mobile seller flow: web-only"
// scope decision.

import 'package:atpost_app/data/models/b2b.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class B2BRepository {
  B2BRepository(this._api);

  final ApiClient _api;

  /// Lists the user's active org memberships. Empty list when the
  /// user is a pure retail buyer — the UI hides the selector then.
  Future<List<Organization>> myOrganizations() async {
    final res = await _api.get('/v1/commerce/organizations/me');
    final raw = res.data['data'];
    if (raw is! Map) return const [];
    final list = (raw['organizations'] as List?) ?? const [];
    return list
        .whereType<Map>()
        .map((m) => Organization.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  /// Tier ladder for a variant. Empty list means flat
  /// variant.selling_price applies; the PDP renders no ladder section
  /// when this is empty. Public endpoint — no auth state required.
  Future<List<PriceTier>> variantPriceTiers(String variantId) async {
    final res = await _api.get('/v1/commerce/variants/$variantId/price-tiers');
    final raw = res.data['data'];
    if (raw is! Map) return const [];
    final list = (raw['tiers'] as List?) ?? const [];
    return list
        .whereType<Map>()
        .map((m) => PriceTier.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  // ─── RFQ buyer surface ─────────────────────────────────────────

  /// The buyer's RFQ inbox — every RFQ they've initiated, newest first.
  Future<List<RFQ>> myRFQs() async {
    final res = await _api.get('/v1/commerce/rfqs');
    final raw = res.data['data'];
    if (raw is! Map) return const [];
    final list = (raw['rfqs'] as List?) ?? const [];
    return list
        .whereType<Map>()
        .map((m) => RFQ.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  Future<RFQDetail> getRFQ(String rfqId) async {
    final res = await _api.get('/v1/commerce/rfqs/$rfqId');
    return RFQDetail.fromJson(
      Map<String, dynamic>.from(res.data['data'] as Map),
    );
  }

  /// Creates an RFQ. Mobile buyers always supply a seller_id (the PDP
  /// resolves it from the product); the seller checks variant
  /// ownership server-side. `organizationId` is optional and unlocks
  /// the org-context fields on the resulting accept-order.
  Future<RFQ> createRFQ({
    required String sellerId,
    required List<({String variantId, int quantity, String? notes})> items,
    String? message,
    String? organizationId,
  }) async {
    final res = await _api.post(
      '/v1/commerce/rfqs',
      data: {
        'seller_id': sellerId,
        'organization_id': ?organizationId,
        if (message != null && message.isNotEmpty) 'message': message,
        'items': [
          for (final it in items)
            {
              'variant_id': it.variantId,
              'quantity': it.quantity,
              if (it.notes != null && it.notes!.isNotEmpty) 'notes': it.notes,
            },
        ],
      },
    );
    final data = Map<String, dynamic>.from(res.data['data'] as Map);
    return RFQ.fromJson(Map<String, dynamic>.from(data['rfq'] as Map));
  }

  /// Accepts a seller's quote, returning the freshly-created order.
  /// Backend bypasses priceCart so the buyer pays exactly what was
  /// quoted; the order detail screen then drives the Razorpay flow if
  /// payment method is prepaid.
  Future<Order> acceptQuote({
    required String rfqId,
    required String quoteId,
    required String addressId,
    String paymentMethod = 'prepaid',
    String? poNumber,
    String? costCenter,
    String? invoiceEmail,
  }) async {
    final res = await _api.post(
      '/v1/commerce/rfqs/$rfqId/quotes/$quoteId/accept',
      data: {
        'address_id': addressId,
        'payment_method': paymentMethod,
        if (poNumber != null && poNumber.isNotEmpty) 'po_number': poNumber,
        if (costCenter != null && costCenter.isNotEmpty) 'cost_center': costCenter,
        if (invoiceEmail != null && invoiceEmail.isNotEmpty)
          'invoice_email': invoiceEmail,
      },
    );
    return Order.fromJson(Map<String, dynamic>.from(res.data['data'] as Map));
  }

  Future<void> rejectRFQ(String rfqId, {String? reason}) async {
    await _api.post(
      '/v1/commerce/rfqs/$rfqId/reject',
      data: reason != null && reason.isNotEmpty ? {'reason': reason} : <String, dynamic>{},
    );
  }
}

final b2bRepositoryProvider = Provider<B2BRepository>((ref) {
  return B2BRepository(ref.watch(apiClientProvider));
});

// ─── Riverpod accessors ─────────────────────────────────────────

final myOrganizationsProvider = FutureProvider.autoDispose<List<Organization>>((ref) async {
  return ref.watch(b2bRepositoryProvider).myOrganizations();
});

final variantPriceTiersProvider =
    FutureProvider.autoDispose.family<List<PriceTier>, String>((ref, variantId) async {
  return ref.watch(b2bRepositoryProvider).variantPriceTiers(variantId);
});

final myRFQsProvider = FutureProvider.autoDispose<List<RFQ>>((ref) async {
  return ref.watch(b2bRepositoryProvider).myRFQs();
});

final rfqDetailProvider =
    FutureProvider.autoDispose.family<RFQDetail, String>((ref, rfqId) async {
  return ref.watch(b2bRepositoryProvider).getRFQ(rfqId);
});
