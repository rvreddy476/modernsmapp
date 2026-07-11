// Contract tests for FoodRewardsRepository against the food-service
// rewards endpoints (handler_rewards.go): request shapes + envelope
// parsing must not drift.
import 'package:feature_figo/data/figo_rewards_repository.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import 'helpers.dart';

void main() {
  late MockApiClient api;
  late FoodRewardsRepository repo;

  setUp(() {
    api = MockApiClient();
    repo = FoodRewardsRepository(api);
  });

  group('getLoyalty', () {
    test('parses balance + ledger from the data envelope', () async {
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
                'order_id': 'o1',
              },
              {'id': 'l2', 'delta': -500, 'reason': 'Redeemed'},
            ],
          },
        }),
      );

      final snapshot = await repo.getLoyalty();

      expect(snapshot.balance.pointsBalance, 1250);
      expect(snapshot.balance.lifetimeEarned, 4300);
      expect(snapshot.balance.tier, 'gold');
      expect(snapshot.ledger, hasLength(2));
      expect(snapshot.ledger.first.reason, 'Order FG-1001');
      expect(snapshot.ledger.first.createdAt, isNotNull);
      expect(snapshot.ledger.last.delta, -500);
      expect(snapshot.ledger.last.orderId, isNull);
    });

    test('tolerates an empty payload with safe defaults', () async {
      when(() => api.get('/v1/food/me/loyalty'))
          .thenAnswer((_) async => ok({'data': <String, dynamic>{}}));

      final snapshot = await repo.getLoyalty();

      expect(snapshot.balance.pointsBalance, 0);
      expect(snapshot.balance.tier, 'bronze');
      expect(snapshot.ledger, isEmpty);
    });
  });

  group('redeemLoyalty', () {
    test('posts points (omitting order_id when null) and parses balance',
        () async {
      when(() => api.post('/v1/food/me/loyalty/redeem',
          data: any(named: 'data'))).thenAnswer(
        (_) async => ok({
          'data': {
            'user_id': 'u1',
            'points_balance': 750,
            'lifetime_earned': 4300,
            'tier': 'gold',
          },
        }),
      );

      final balance = await repo.redeemLoyalty(points: 500);

      expect(balance.pointsBalance, 750);
      final sent = verify(() => api.post('/v1/food/me/loyalty/redeem',
              data: captureAny(named: 'data')))
          .captured
          .single as Map<String, dynamic>;
      expect(sent['points'], 500);
      expect(sent.containsKey('order_id'), isFalse);
    });

    test('forwards order_id when provided', () async {
      when(() => api.post('/v1/food/me/loyalty/redeem',
          data: any(named: 'data'))).thenAnswer(
        (_) async => ok({
          'data': {'points_balance': 0},
        }),
      );

      await repo.redeemLoyalty(points: 100, orderId: 'o42');

      final sent = verify(() => api.post('/v1/food/me/loyalty/redeem',
              data: captureAny(named: 'data')))
          .captured
          .single as Map<String, dynamic>;
      expect(sent['order_id'], 'o42');
    });
  });

  group('referrals', () {
    test('getReferralCode unwraps the code', () async {
      when(() => api.get('/v1/food/me/referral')).thenAnswer(
        (_) async => ok({
          'data': {'code': 'FIGO-XYZ'},
        }),
      );

      expect(await repo.getReferralCode(), 'FIGO-XYZ');
    });

    test('applyReferralCode posts the trimmed code payload', () async {
      when(() => api.post('/v1/food/me/referral/apply',
              data: any(named: 'data')))
          .thenAnswer((_) async => ok({'data': <String, dynamic>{}}));

      await repo.applyReferralCode('FRIEND-1');

      final sent = verify(() => api.post('/v1/food/me/referral/apply',
              data: captureAny(named: 'data')))
          .captured
          .single as Map<String, dynamic>;
      expect(sent, {'code': 'FRIEND-1'});
    });
  });
}
