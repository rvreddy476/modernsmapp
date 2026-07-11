// The realtime layer must degrade gracefully: any failure minting the
// SSE topic token means "no push session" (the screen falls back to
// REST polling) — never an error surfaced to the UI.
import 'package:atpost_network/api_client.dart';
import 'package:feature_figo/providers/figo_realtime_provider.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import 'helpers.dart';

void main() {
  late MockApiClient api;
  late ProviderContainer container;

  setUp(() {
    api = MockApiClient();
    container = ProviderContainer(
      overrides: [apiClientProvider.overrideWithValue(api)],
    );
    addTearDown(container.dispose);
  });

  /// Riverpod's StreamProvider never closes its exposed stream (it only
  /// ends on dispose), so collect whatever lands within a short window
  /// instead of awaiting stream completion.
  Future<List<FoodOrderEvent>> collectEvents() async {
    final events = <FoodOrderEvent>[];
    final sub = container.listen(foodOrderPushProvider, (_, next) {
      next.whenData(events.add);
    });
    addTearDown(sub.close);
    await Future<void>.delayed(const Duration(milliseconds: 100));
    return events;
  }

  test('token endpoint failure degrades to an empty push stream', () async {
    when(() => api.post('/v1/food/realtime/token'))
        .thenThrow(httpError(503, path: '/v1/food/realtime/token'));

    expect(await collectEvents(), isEmpty);
  });

  test('token response without food.order.* topics yields no session',
      () async {
    when(() => api.post('/v1/food/realtime/token')).thenAnswer(
      (_) async => ok({
        'data': {
          'token': 'hmac-token',
          'topics': ['food.restaurant.r1', 'food.admin.dashboard'],
        },
      }),
    );

    expect(await collectEvents(), isEmpty);
  });

  test('empty token yields no session', () async {
    when(() => api.post('/v1/food/realtime/token')).thenAnswer(
      (_) async => ok({
        'data': {'token': '', 'topics': <String>[]},
      }),
    );

    expect(await collectEvents(), isEmpty);
  });
}
