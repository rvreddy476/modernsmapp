// MopeduRepository contract tests vs rider-service routes
// (/v1/rider/cities|estimate|rides|rides/me|rides/:id/rate — all live).
import 'package:atpost_network/api_client.dart';
import 'package:mopedu_domain/mopedu.dart';
import 'package:mopedu_domain/mopedu_repository.dart';
import 'package:dio/dio.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

class MockApiClient extends Mock implements ApiClient {}

class MockStorage extends Mock implements FlutterSecureStorage {}

Response<dynamic> ok(Object? body) => Response(
      requestOptions: RequestOptions(path: '/'),
      statusCode: 200,
      data: body,
    );

void main() {
  late MockApiClient api;
  late MopeduRepository repo;

  const pickup = RidePoint(lat: 17.4, lng: 78.4);
  const drop = RidePoint(lat: 17.5, lng: 78.5);

  setUp(() {
    api = MockApiClient();
    repo = MopeduRepository(api, storage: MockStorage());
  });

  test('estimateFare posts the flat lat/lng payload', () async {
    when(() => api.post('/v1/rider/estimate', data: any(named: 'data')))
        .thenAnswer((_) async => ok({
              'data': {'currency': 'INR'},
            }));

    await repo.estimateFare(
      pickup: pickup,
      drop: drop,
      vehicleType: 'moped',
      cityId: 'hyd',
    );

    final sent = verify(() =>
            api.post('/v1/rider/estimate', data: captureAny(named: 'data')))
        .captured
        .single as Map<String, dynamic>;
    expect(sent['pickup_lat'], 17.4);
    expect(sent['drop_lng'], 78.5);
    expect(sent['vehicle_type'], 'moped');
    expect(sent['city_id'], 'hyd');
  });

  test('createRide nests points, sends idempotency key, omits schedule',
      () async {
    when(() => api.post('/v1/rider/rides', data: any(named: 'data')))
        .thenAnswer((_) async => ok({
              'data': {'id': 'ride-1', 'status': 'REQUESTED'},
            }));

    final ride = await repo.createRide(
      pickup: pickup,
      drop: drop,
      vehicleType: 'moped',
      cityId: 'hyd',
      paymentMethod: 'wallet',
      idempotencyKey: 'idem-123',
    );

    expect(ride.id, 'ride-1');
    final sent = verify(() =>
            api.post('/v1/rider/rides', data: captureAny(named: 'data')))
        .captured
        .single as Map<String, dynamic>;
    expect(sent['pickup'], isA<Map<String, dynamic>>());
    expect(sent['idempotency_key'], 'idem-123');
    expect(sent.containsKey('scheduled_for'), isFalse);
  });

  test('getMyRides parses items + meta cursor', () async {
    when(() => api.get('/v1/rider/rides/me',
            queryParameters: any(named: 'queryParameters')))
        .thenAnswer((_) async => ok({
              'data': [
                {'id': 'r1', 'status': 'COMPLETED'},
                {'id': 'r2', 'status': 'CANCELLED'},
              ],
              'meta': {'next_cursor': 'abc'},
            }));

    final page = await repo.getMyRides();

    expect(page.items, hasLength(2));
    expect(page.nextCursor, 'abc');
  });

  test('rateRide returns true on success and false on failure — never throws',
      () async {
    when(() => api.post('/v1/rider/rides/r1/rate', data: any(named: 'data')))
        .thenAnswer((_) async => ok({'data': <String, dynamic>{}}));
    expect(await repo.rateRide(rideId: 'r1', stars: 5), isTrue);

    when(() => api.post('/v1/rider/rides/r2/rate', data: any(named: 'data')))
        .thenThrow(DioException(
      requestOptions: RequestOptions(path: '/v1/rider/rides/r2/rate'),
    ));
    expect(await repo.rateRide(rideId: 'r2', stars: 1), isFalse);
  });

  test('listCities parses the data list', () async {
    when(() => api.get('/v1/rider/cities')).thenAnswer((_) async => ok({
          'data': [
            {'id': 'hyd', 'name': 'Hyderabad'},
          ],
        }));

    final cities = await repo.listCities();

    expect(cities, hasLength(1));
  });
}
