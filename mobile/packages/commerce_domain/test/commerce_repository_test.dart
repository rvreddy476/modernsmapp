// Contract tests for the customer-flow fixes: server-side checkout
// quote (with client fallback) and the wishlist endpoints that
// commerce-service now serves.
import 'package:commerce_domain/data/commerce_repository.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import 'helpers.dart';

void main() {
  late MockApiClient api;
  late CommerceRepository repo;

  setUp(() {
    api = MockApiClient();
    repo = CommerceRepository(api);
  });

  group('checkoutQuote', () {
    test('uses POST /checkout/quote and maps the server pricing', () async {
      when(() => api.post('/v1/commerce/checkout/quote',
              data: any(named: 'data')))
          .thenAnswer((_) async => ok({
                'data': {
                  'subtotal': 1000.0,
                  'coupon_discount': 100.0,
                  'shipping': 49.0,
                  'tax': 180.0,
                  'grand_total': 1129.0,
                  'cod_eligible': false,
                  'serviceable': true,
                },
              }));

      final quote = await repo.checkoutQuote(
        addressId: 'addr-1',
        paymentMethod: 'cod',
        couponCode: 'SAVE10',
      );

      expect(quote.subtotal, 1000);
      expect(quote.discountTotal, 100);
      expect(quote.shippingTotal, 49);
      expect(quote.taxTotal, 180);
      expect(quote.grandTotal, 1129);
      expect(quote.isCodAllowed, isFalse);

      final sent = verify(() => api.post('/v1/commerce/checkout/quote',
              data: captureAny(named: 'data')))
          .captured
          .single as Map<String, dynamic>;
      expect(sent['address_id'], 'addr-1');
      expect(sent['payment_method'], 'cod');
      expect(sent['coupon_code'], 'SAVE10');
    });

    test('falls back to a client cart projection when the endpoint errors',
        () async {
      when(() => api.post('/v1/commerce/checkout/quote',
              data: any(named: 'data')))
          .thenThrow(httpError(500, path: '/v1/commerce/checkout/quote'));
      when(() => api.get('/v1/commerce/cart')).thenAnswer((_) async => ok({
            'data': {
              'id': 'cart-1',
              'items': <dynamic>[],
              'subtotal': 500.0,
              'tax_total': 90.0,
              'shipping_total': 0.0,
              'discount_total': 0.0,
              'grand_total': 590.0,
            },
          }));

      final quote = await repo.checkoutQuote(
        addressId: 'addr-1',
        paymentMethod: 'cod',
      );

      expect(quote.grandTotal, 590);
      expect(quote.isCodAllowed, isTrue); // under the ₹5000 COD cap
    });
  });

  group('wishlist', () {
    test('getWishlist parses the items envelope with product snapshots',
        () async {
      when(() => api.get('/v1/commerce/wishlist')).thenAnswer((_) async => ok({
            'data': {
              'items': [
                {
                  'product_id': 'p1',
                  'saved_at': '2026-07-12T10:00:00Z',
                  'product': {
                    'id': 'p1',
                    'title': 'Cotton Kurta',
                    'selling_price': 799.0,
                    'mrp': 999.0,
                  },
                },
              ],
            },
          }));

      final list = await repo.getWishlist();

      expect(list, hasLength(1));
      expect(list.single.productId, 'p1');
      expect(list.single.productSnapshot.title, 'Cotton Kurta');
      expect(list.single.productSnapshot.sellingPrice, 799);
      expect(list.single.productSnapshot.discountPct, 20);
    });

    test('add posts product_id; remove deletes by path', () async {
      when(() => api.post('/v1/commerce/wishlist', data: any(named: 'data')))
          .thenAnswer((_) async => ok({
                'data': {'saved': true},
              }));
      when(() => api.delete('/v1/commerce/wishlist/p1'))
          .thenAnswer((_) async => ok({
                'data': {'removed': true},
              }));

      await repo.addToWishlist('p1');
      await repo.removeFromWishlist('p1');

      final sent = verify(() => api.post('/v1/commerce/wishlist',
              data: captureAny(named: 'data')))
          .captured
          .single as Map<String, dynamic>;
      expect(sent['product_id'], 'p1');
      verify(() => api.delete('/v1/commerce/wishlist/p1')).called(1);
    });
  });
}
