// Rewards screen render test: loyalty balance, ledger, and referral
// code sections over the real repo parsing canned service payloads.
import 'package:feature_figo/data/figo_rewards_repository.dart';
import 'package:feature_figo/figo_rewards_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import 'helpers.dart';

void main() {
  late MockApiClient api;

  setUp(() {
    api = MockApiClient();
    when(() => api.get('/v1/food/me/loyalty')).thenAnswer(
      (_) async => ok({
        'data': {
          'balance': {
            'user_id': 'u1',
            'points_balance': 1250,
            'lifetime_earned': 4300,
            'tier': 'gold',
          },
          'ledger': [
            {
              'id': 'l1',
              'delta': 120,
              'reason': 'Order FG-1001',
              'created_at': '2026-07-01T10:00:00Z',
            },
          ],
        },
      }),
    );
    when(() => api.get('/v1/food/me/referral')).thenAnswer(
      (_) async => ok({
        'data': {'code': 'FIGO-XYZ'},
      }),
    );
  });

  testWidgets('renders balance, ledger and referral code', (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          figoRewardsRepositoryProvider
              .overrideWithValue(FoodRewardsRepository(api)),
        ],
        child: const MaterialApp(home: FigoRewardsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('FiGo rewards'), findsOneWidget);
    expect(find.text('Balance'), findsWidgets);
    expect(find.textContaining('1250'), findsWidgets);
    expect(find.text('Order FG-1001'), findsOneWidget);

    // Referral code lives on the second tab.
    await tester.tap(find.text('Referral'));
    await tester.pumpAndSettle();
    expect(find.textContaining('FIGO-XYZ'), findsWidgets);
  });
}
