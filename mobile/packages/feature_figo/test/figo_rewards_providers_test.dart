// Provider-layer tests: loyalty load, redeem (with balance refresh),
// and referral apply — over a mocked ApiClient so the whole
// repo+provider stack is exercised.
import 'package:feature_figo/data/figo_rewards_repository.dart';
import 'package:feature_figo/providers/figo_rewards_providers.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import 'helpers.dart';

void main() {
  late MockApiClient api;
  late ProviderContainer container;

  setUp(() {
    api = MockApiClient();
    container = ProviderContainer(overrides: [
      figoRewardsRepositoryProvider
          .overrideWithValue(FoodRewardsRepository(api)),
    ]);
    addTearDown(container.dispose);
  });

  Map<String, dynamic> loyaltyBody(int points) => {
        'data': {
          'balance': {'user_id': 'u1', 'points_balance': points},
          'ledger': const [],
        },
      };

  test('foodLoyaltyProvider loads the snapshot', () async {
    when(() => api.get('/v1/food/me/loyalty'))
        .thenAnswer((_) async => ok(loyaltyBody(900)));

    // Keep the autoDispose provider alive across the await.
    final sub = container.listen(foodLoyaltyProvider, (_, _) {});
    addTearDown(sub.close);

    final snapshot = await container.read(foodLoyaltyProvider.future);
    expect(snapshot.balance.pointsBalance, 900);
  });

  test('redeem success refreshes the loyalty balance', () async {
    when(() => api.get('/v1/food/me/loyalty'))
        .thenAnswer((_) async => ok(loyaltyBody(900)));
    when(() => api.post('/v1/food/me/loyalty/redeem',
            data: any(named: 'data')))
        .thenAnswer((_) async => ok({
              'data': {'points_balance': 400},
            }));

    final sub = container.listen(foodLoyaltyProvider, (_, _) {});
    addTearDown(sub.close);
    await container.read(foodLoyaltyProvider.future);

    final okFlag =
        await container.read(foodLoyaltyRedeemProvider.notifier).redeem(500);

    expect(okFlag, isTrue);
    expect(container.read(foodLoyaltyRedeemProvider), const AsyncData<void>(null));
    // The redeem invalidated foodLoyaltyProvider → a second GET fires.
    await container.read(foodLoyaltyProvider.future);
    verify(() => api.get('/v1/food/me/loyalty')).called(2);
  });

  test('redeem failure surfaces AsyncError and returns false', () async {
    when(() => api.post('/v1/food/me/loyalty/redeem',
            data: any(named: 'data')))
        .thenThrow(httpError(422, path: '/v1/food/me/loyalty/redeem'));

    final okFlag =
        await container.read(foodLoyaltyRedeemProvider.notifier).redeem(99999);

    expect(okFlag, isFalse);
    expect(container.read(foodLoyaltyRedeemProvider), isA<AsyncError<void>>());
  });

  test('referral apply trims the code and reports success', () async {
    when(() => api.post('/v1/food/me/referral/apply',
            data: any(named: 'data')))
        .thenAnswer((_) async => ok({'data': <String, dynamic>{}}));

    final okFlag = await container
        .read(foodReferralApplyProvider.notifier)
        .submit('  FRIEND-1  ');

    expect(okFlag, isTrue);
    final sent = verify(() => api.post('/v1/food/me/referral/apply',
            data: captureAny(named: 'data')))
        .captured
        .single as Map<String, dynamic>;
    expect(sent['code'], 'FRIEND-1');
  });

  test('referral apply failure returns false with AsyncError state', () async {
    when(() => api.post('/v1/food/me/referral/apply',
            data: any(named: 'data')))
        .thenThrow(httpError(409, path: '/v1/food/me/referral/apply'));

    final okFlag = await container
        .read(foodReferralApplyProvider.notifier)
        .submit('SELF-REFERRAL');

    expect(okFlag, isFalse);
    expect(
        container.read(foodReferralApplyProvider), isA<AsyncError<void>>());
  });
}
