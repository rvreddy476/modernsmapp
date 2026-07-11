// Locks the P0-1 dating-service contract realignments in
// PulseRepository: the legacy "decision" model routes to the
// spark/stash/pass triad, profile upsert is POST (not PUT), unmatch is
// POST /matches/:id/close (not DELETE), and likes-received maps to
// incoming sparks.
import 'package:feature_pulse/data/pulse_repository.dart';
import 'package:feature_pulse/services/pulse_api_client.dart';
import 'package:feature_pulse/services/pulse_auth_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

class MockPulseApiClient extends Mock implements PulseApiClient {}

class MockPulseAuthService extends Mock implements PulseAuthService {}

Response<dynamic> ok(Object? body) => Response(
      requestOptions: RequestOptions(path: '/'),
      statusCode: 200,
      data: body,
    );

void main() {
  late MockPulseApiClient api;
  late PulseRepository repo;

  setUp(() {
    api = MockPulseApiClient();
    repo = PulseRepository(api, MockPulseAuthService());
  });

  group('makeDecision', () {
    test('spark posts to /v1/dating/sparks and adapts the match flag',
        () async {
      when(() => api.post('/v1/dating/sparks', data: any(named: 'data')))
          .thenAnswer(
        (_) async => ok({
          'data': {
            'spark': {'id': 'sp1'},
            'matched': true,
            'match_id': 'm42',
          },
        }),
      );

      final result =
          await repo.makeDecision(targetUserId: 'u9', decision: 'spark');

      expect(result.matchId, 'm42');
      final sent = verify(() =>
              api.post('/v1/dating/sparks', data: captureAny(named: 'data')))
          .captured
          .single as Map<String, dynamic>;
      expect(sent['to_user_id'], 'u9');
      expect(sent['target_kind'], 'profile');
    });

    test('legacy "like" verb rides the spark route', () async {
      when(() => api.post('/v1/dating/sparks', data: any(named: 'data')))
          .thenAnswer(
        (_) async => ok({
          'data': {
            'spark': {'id': 'sp2'},
            'matched': false,
          },
        }),
      );

      final result =
          await repo.makeDecision(targetUserId: 'u9', decision: 'like');

      expect(result.matchId, isNull);
      verify(() => api.post('/v1/dating/sparks', data: any(named: 'data')))
          .called(1);
    });

    test('stash posts the candidate to /v1/dating/stash', () async {
      when(() => api.post('/v1/dating/stash', data: any(named: 'data')))
          .thenAnswer((_) async => ok({'data': <String, dynamic>{}}));

      await repo.makeDecision(targetUserId: 'u9', decision: 'stash');

      final sent = verify(() =>
              api.post('/v1/dating/stash', data: captureAny(named: 'data')))
          .captured
          .single as Map<String, dynamic>;
      expect(sent, {'candidate_id': 'u9'});
    });

    test('pass is local-only — no endpoint call', () async {
      await repo.makeDecision(targetUserId: 'u9', decision: 'pass');

      verifyNever(() => api.post(any(), data: any(named: 'data')));
    });
  });

  test('getProfile returns null on 404 instead of throwing', () async {
    when(() => api.get('/v1/dating/profile')).thenThrow(
      DioException(
        requestOptions: RequestOptions(path: '/v1/dating/profile'),
        response: Response(
          requestOptions: RequestOptions(path: '/v1/dating/profile'),
          statusCode: 404,
        ),
        type: DioExceptionType.badResponse,
      ),
    );

    expect(await repo.getProfile(), isNull);
  });

  test('updateProfile uses POST upsert semantics', () async {
    when(() => api.post('/v1/dating/profile', data: any(named: 'data')))
        .thenAnswer(
      (_) async => ok({
        'data': {'user_id': 'u1', 'display_name': 'Ravi'},
      }),
    );

    await repo.updateProfile({'display_name': 'Ravi'});

    verify(() => api.post('/v1/dating/profile', data: any(named: 'data')))
        .called(1);
  });

  test('unmatch closes via POST /matches/:id/close', () async {
    when(() => api.post('/v1/dating/matches/m1/close'))
        .thenAnswer((_) async => ok({'data': <String, dynamic>{}}));

    await repo.unmatch('m1');

    verify(() => api.post('/v1/dating/matches/m1/close')).called(1);
  });

  test('likes received maps to incoming sparks', () async {
    when(() => api.get('/v1/dating/sparks/incoming')).thenAnswer(
      (_) async => ok({
        'data': [
          {'id': 'sp1', 'from_user_id': 'u7'},
        ],
      }),
    );

    final likes = await repo.getLikesReceived();

    expect(likes, hasLength(1));
    verify(() => api.get('/v1/dating/sparks/incoming')).called(1);
  });
}
