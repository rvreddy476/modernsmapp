// Delivery-workspace tests: availability toggle, the dispatch-offer
// inbox (the only way work arrives — 25s TTL server-side), and
// status-aware assignment actions matching food-service's
// deliveryTransitionAllowed chain.
import 'package:atpost_network/api_client.dart';
import 'package:feature_figo/figo_home_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import 'helpers.dart';

void main() {
  late MockApiClient api;

  setUp(() {
    api = MockApiClient();

    // Catch-alls first; specifics override (mocktail: last registered wins).
    when(() => api.get(any(),
            queryParameters: any(named: 'queryParameters'),
            options: any(named: 'options'),
            cancelToken: any(named: 'cancelToken')))
        .thenAnswer((inv) async => throw httpError(404,
            path: inv.positionalArguments.first as String));
    when(() => api.post(any(),
            data: any(named: 'data'),
            queryParameters: any(named: 'queryParameters'),
            options: any(named: 'options'),
            cancelToken: any(named: 'cancelToken')))
        .thenAnswer((inv) async => throw httpError(404,
            path: inv.positionalArguments.first as String));

    // Customer-surface loads fire on every snapshot (all roles).
    when(() => api.get('/v1/food/home')).thenAnswer(
      (_) async => ok({
        'data': {'cuisines': <dynamic>[], 'nearby_restaurants': <dynamic>[]},
      }),
    );
    for (final path in ['/v1/food/cart', '/v1/food/orders', '/v1/food/addresses']) {
      when(() => api.get(path)).thenAnswer(
        (_) async => ok({
          'data': {'items': <dynamic>[]},
        }),
      );
    }

    // Delivery workspace.
    when(() => api.get('/v1/food/delivery/profile')).thenAnswer(
      (_) async => ok({
        'data': {'status': 'APPROVED', 'is_online': true},
      }),
    );
    when(() => api.get('/v1/food/delivery/assignments/current')).thenAnswer(
      (_) async => ok({
        'data': {
          'id': 'as1',
          'order_number': 'FG-2002',
          'restaurant_name': 'Spice Villa',
          'status': 'PICKED_UP',
          'delivery_partner_payout': 45,
        },
      }),
    );
    when(() => api.get('/v1/food/delivery/earnings')).thenAnswer(
      (_) async => ok({
        'data': {'earnings_today': 320, 'total_earnings': 5400},
      }),
    );
    when(() => api.get('/v1/food/delivery/offers/me')).thenAnswer(
      (_) async => ok({
        'data': {
          'offers': [
            {
              'id': 'off1',
              'order_id': 'o77',
              'distance_km': 2.4,
              // Deterministically past: no expiry-refresh Timer gets
              // scheduled, so pumpAndSettle/teardown stay clean.
              'expires_at': '2020-01-01T00:00:25Z',
            },
          ],
        },
      }),
    );
    when(() => api.post('/v1/food/delivery/offers/off1/accept',
            options: any(named: 'options')))
        .thenAnswer((_) async => ok({'data': <String, dynamic>{}}));
  });

  Future<void> pumpDelivery(WidgetTester tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [apiClientProvider.overrideWithValue(api)],
        child: const MaterialApp(home: FigoHomeScreen()),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Delivery'));
    await tester.pumpAndSettle();
  }

  testWidgets(
      'shows availability, the offers inbox, and only the legal transition',
      (tester) async {
    await pumpDelivery(tester);

    expect(find.text('Online — receiving offers'), findsOneWidget);
    expect(find.textContaining('2.4 km pickup'), findsOneWidget);

    // PICKED_UP may only advance to ARRIVED_AT_CUSTOMER — never straight
    // to DELIVERED (server rejects that transition).
    expect(find.text('Arrived at customer'), findsOneWidget);
    expect(find.text('Delivered'), findsNothing);
  });

  testWidgets('accepting an offer posts to the offer accept endpoint',
      (tester) async {
    await pumpDelivery(tester);

    await tester.ensureVisible(find.text('Accept'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Accept'));
    await tester.pumpAndSettle();
    await tester.pump(const Duration(seconds: 5));
    await tester.pumpAndSettle();

    verify(() => api.post('/v1/food/delivery/offers/off1/accept',
        options: any(named: 'options'))).called(1);
  });
}
