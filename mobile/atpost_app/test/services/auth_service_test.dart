import 'package:atpost_app/services/auth_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

class _MockDio extends Mock implements Dio {}

class _MockFlutterSecureStorage extends Mock implements FlutterSecureStorage {}

Future<void> _settleAsyncWork() async {
  await Future<void>.delayed(Duration.zero);
  await Future<void>.delayed(Duration.zero);
}

void main() {
  late _MockDio dio;
  late _MockFlutterSecureStorage storage;

  setUp(() {
    dio = _MockDio();
    storage = _MockFlutterSecureStorage();

    when(
      () => storage.write(
        key: any(named: 'key'),
        value: any(named: 'value'),
      ),
    ).thenAnswer((_) async {});
    when(() => storage.delete(key: any(named: 'key'))).thenAnswer((_) async {});
  });

  group('AuthService', () {
    test('restoreSession clears incomplete persisted auth state', () async {
      when(
        () => storage.read(key: 'auth_user_id'),
      ).thenAnswer((_) async => 'user-1');
      when(
        () => storage.read(key: 'auth_token'),
      ).thenAnswer((_) async => '   ');
      when(
        () => storage.read(key: 'auth_refresh_token'),
      ).thenAnswer((_) async => 'stale-refresh');

      final service = AuthService(storage: storage, dio: dio);

      await service.restoreSession();

      expect(service.isAuthenticated, isFalse);
      expect(service.userId, isNull);
      expect(service.token, isNull);
      expect(service.refreshToken, isNull);
      verify(() => storage.delete(key: 'auth_user_id')).called(1);
      verify(() => storage.delete(key: 'auth_token')).called(1);
      verify(() => storage.delete(key: 'auth_refresh_token')).called(1);
    });

    test(
      'login clears a stale refresh token when the response omits one',
      () async {
        when(
          () => dio.post('/v1/auth/login', data: any(named: 'data')),
        ).thenAnswer(
          (_) async => Response<Map<String, dynamic>>(
            requestOptions: RequestOptions(path: '/v1/auth/login'),
            data: {
              'data': {'user_id': 'user-1', 'access_token': 'fresh-token'},
            },
          ),
        );

        final service = AuthService(storage: storage, dio: dio);
        service.setSession(
          userId: 'old-user',
          token: 'old-token',
          refreshToken: 'stale-refresh',
        );
        await _settleAsyncWork();

        reset(storage);
        when(
          () => storage.write(
            key: any(named: 'key'),
            value: any(named: 'value'),
          ),
        ).thenAnswer((_) async {});
        when(
          () => storage.delete(key: any(named: 'key')),
        ).thenAnswer((_) async {});

        final success = await service.login('user@example.com', 'secret');

        expect(success, isTrue);
        expect(service.userId, 'user-1');
        expect(service.token, 'fresh-token');
        expect(service.refreshToken, isNull);
        verify(
          () => storage.write(key: 'auth_user_id', value: 'user-1'),
        ).called(1);
        verify(
          () => storage.write(key: 'auth_token', value: 'fresh-token'),
        ).called(1);
        verify(() => storage.delete(key: 'auth_refresh_token')).called(1);
      },
    );

    test(
      'logout only clears auth keys instead of deleting all secure storage',
      () async {
        final service = AuthService(storage: storage, dio: dio);
        service.setSession(
          userId: 'user-1',
          token: 'access-token',
          refreshToken: 'refresh-token',
        );
        await _settleAsyncWork();

        reset(storage);
        when(
          () => storage.delete(key: any(named: 'key')),
        ).thenAnswer((_) async {});

        service.logout();
        await _settleAsyncWork();

        expect(service.isAuthenticated, isFalse);
        expect(service.userId, isNull);
        expect(service.token, isNull);
        expect(service.refreshToken, isNull);
        verify(() => storage.delete(key: 'auth_user_id')).called(1);
        verify(() => storage.delete(key: 'auth_token')).called(1);
        verify(() => storage.delete(key: 'auth_refresh_token')).called(1);
        verifyNoMoreInteractions(storage);
      },
    );
  });
}
