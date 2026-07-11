// Customer-home render test over canned food-service payloads: the
// screen must fetch home/cart/orders/addresses (and only those — role
// surfaces are gated) and render restaurants, cuisines, and the active
// order from the envelope shapes the Go service actually returns.
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

    // Catch-alls first (mocktail picks the most recent matching stub):
    // any un-stubbed endpoint fails like the server would, exercising
    // the screen's _safeLoad fallbacks.
    when(() => api.get(any(),
            queryParameters: any(named: 'queryParameters'),
            options: any(named: 'options'),
            cancelToken: any(named: 'cancelToken')))
        .thenAnswer((inv) async =>
            throw httpError(404, path: inv.positionalArguments.first as String));
    when(() => api.post(any(),
            data: any(named: 'data'),
            queryParameters: any(named: 'queryParameters'),
            options: any(named: 'options'),
            cancelToken: any(named: 'cancelToken')))
        .thenAnswer((inv) async =>
            throw httpError(404, path: inv.positionalArguments.first as String));

    when(() => api.get('/v1/food/home')).thenAnswer(
      (_) async => ok({
        'data': {
          'cuisines': [
            {'name': 'Biryani'},
            {'name': 'Pizza'},
          ],
          'nearby_restaurants': [
            {
              'id': 'r1',
              'name': 'Spice Villa',
              'hero_image_url': '',
              'cuisines': ['Biryani', 'Andhra'],
              'avg_rating': 4.5,
              'estimated_delivery': '20-30 min',
              'delivery_fee_estimate': 29,
            },
          ],
        },
      }),
    );
    when(() => api.get('/v1/food/cart')).thenAnswer(
      (_) async => ok({
        'data': {
          'items': [
            {'name': 'Chicken Biryani', 'quantity': 2, 'line_total': 398},
          ],
          'totals': {'final_amount': 398},
        },
      }),
    );
    when(() => api.get('/v1/food/orders')).thenAnswer(
      (_) async => ok({
        'data': {
          'items': [
            {
              'id': 'o1',
              'order_number': 'FG-1001',
              'restaurant_name': 'Spice Villa',
              'status': 'PREPARING',
              'totals': {'final_amount': 398},
            },
          ],
        },
      }),
    );
    when(() => api.get('/v1/food/addresses')).thenAnswer(
      (_) async => ok({
        'data': {
          'items': [
            {
              'id': 'a1',
              'label': 'Home',
              'address_line1': '12 MG Road',
              'city': 'Hyderabad',
              'is_default': true,
            },
          ],
        },
      }),
    );
    when(() => api.get('/v1/food/orders/o1/tracking')).thenAnswer(
      (_) async => ok({
        'data': {
          'order_number': 'FG-1001',
          'status': 'PREPARING',
          'estimated_delivery_minutes': 22,
        },
      }),
    );
  });

  Future<void> pumpHome(WidgetTester tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [apiClientProvider.overrideWithValue(api)],
        child: const MaterialApp(home: FigoHomeScreen()),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets('renders restaurants, cuisines and the active order',
      (tester) async {
    await pumpHome(tester);

    expect(find.text('Spice Villa'), findsWidgets);
    expect(find.text('Biryani'), findsWidgets);
    expect(find.textContaining('FG-1001'), findsWidgets);
    expect(find.textContaining('PREPARING'), findsWidgets);
  });

  testWidgets('customer role never calls partner/delivery/admin endpoints',
      (tester) async {
    await pumpHome(tester);

    verifyNever(() => api.get('/v1/food/partner/restaurants'));
    verifyNever(() => api.get('/v1/food/delivery/profile'));
    verifyNever(() => api.get('/v1/food/admin/dashboard'));
  });
}
