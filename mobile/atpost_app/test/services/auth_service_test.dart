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

        expect(success.success, isTrue);
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

    test('logout revokes the refresh token server-side', () async {
      when(
        () => dio.post(
          '/v1/auth/logout',
          data: any(named: 'data'),
          options: any(named: 'options'),
        ),
      ).thenAnswer(
        (_) async => Response<void>(
          requestOptions: RequestOptions(path: '/v1/auth/logout'),
        ),
      );

      final service = AuthService(storage: storage, dio: dio);
      await service.setSession(
        userId: 'user-1',
        token: 'access-token',
        refreshToken: 'refresh-token',
      );

      service.logout();
      await _settleAsyncWork();

      verify(
        () => dio.post(
          '/v1/auth/logout',
          data: {'refresh_token': 'refresh-token'},
          options: any(named: 'options'),
        ),
      ).called(1);
    });

    test('verifyOtp (2fa) forwards the pending token and mints the session',
        () async {
      when(
        () => dio.post('/v1/auth/verify-2fa', data: any(named: 'data')),
      ).thenAnswer(
        (_) async => Response<Map<String, dynamic>>(
          requestOptions: RequestOptions(path: '/v1/auth/verify-2fa'),
          data: {
            'data': {
              'user': {'id': 'user-9'},
              'tokens': {
                'access_token': 'fresh-access',
                'refresh_token': 'fresh-refresh',
              },
            },
          },
        ),
      );

      final service = AuthService(storage: storage, dio: dio);
      final error = await service.verifyOtp(
        identifier: 'user@example.com',
        code: '123456',
        pendingToken: 'pending-1',
        is2fa: true,
      );

      expect(error, isNull);
      expect(service.isAuthenticated, isTrue);
      expect(service.userId, 'user-9');
      expect(service.token, 'fresh-access');
      expect(service.refreshToken, 'fresh-refresh');

      final sent = verify(
        () => dio.post('/v1/auth/verify-2fa', data: captureAny(named: 'data')),
      ).captured.single as Map<String, dynamic>;
      expect(sent['pending_token'], 'pending-1');
      expect(sent['identifier'], 'user@example.com');
      expect(sent['code'], '123456');
    });

    test('verifyOtp (2fa) treats a token-less 200 as a failure', () async {
      when(
        () => dio.post('/v1/auth/verify-2fa', data: any(named: 'data')),
      ).thenAnswer(
        (_) async => Response<Map<String, dynamic>>(
          requestOptions: RequestOptions(path: '/v1/auth/verify-2fa'),
          data: {
            'data': {'status': 'ok'},
          },
        ),
      );

      final service = AuthService(storage: storage, dio: dio);
      final error = await service.verifyOtp(
        identifier: 'user@example.com',
        code: '123456',
        pendingToken: 'pending-1',
        is2fa: true,
      );

      expect(error, isNotNull);
      expect(service.isAuthenticated, isFalse);
    });

    test('a rejected refresh token (401) clears the local session', () async {
      when(
        () => dio.post('/v1/auth/refresh', data: any(named: 'data')),
      ).thenThrow(
        DioException(
          requestOptions: RequestOptions(path: '/v1/auth/refresh'),
          response: Response<void>(
            requestOptions: RequestOptions(path: '/v1/auth/refresh'),
            statusCode: 401,
          ),
        ),
      );

      final service = AuthService(storage: storage, dio: dio);
      await service.setSession(
        userId: 'user-1',
        token: 'access-token',
        refreshToken: 'revoked-refresh',
      );

      final refreshed = await service.refreshAccessToken();
      await _settleAsyncWork();

      expect(refreshed, isFalse);
      expect(service.isAuthenticated, isFalse);
      expect(service.token, isNull);
      verify(() => storage.delete(key: 'auth_refresh_token')).called(1);
    });

    test('a transient refresh failure (503) keeps the session', () async {
      when(
        () => dio.post('/v1/auth/refresh', data: any(named: 'data')),
      ).thenThrow(
        DioException(
          requestOptions: RequestOptions(path: '/v1/auth/refresh'),
          response: Response<void>(
            requestOptions: RequestOptions(path: '/v1/auth/refresh'),
            statusCode: 503,
          ),
        ),
      );

      final service = AuthService(storage: storage, dio: dio);
      await service.setSession(
        userId: 'user-1',
        token: 'access-token',
        refreshToken: 'refresh-token',
      );

      final refreshed = await service.refreshAccessToken();

      expect(refreshed, isFalse);
      expect(service.isAuthenticated, isTrue);
      expect(service.token, 'access-token');
    });

    test('setSession rejects empty credentials', () async {
      final service = AuthService(storage: storage, dio: dio);

      await service.setSession(userId: '', token: '');

      expect(service.isAuthenticated, isFalse);
      verifyNever(
        () => storage.write(key: any(named: 'key'), value: any(named: 'value')),
      );
    });
  });
}
