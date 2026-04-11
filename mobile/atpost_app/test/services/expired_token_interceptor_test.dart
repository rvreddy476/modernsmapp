import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/interceptors/expired_token_interceptor.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

class _MockAuthService extends Mock implements AuthService {}

class _MockDio extends Mock implements Dio {}

class _RecordingErrorHandler extends ErrorInterceptorHandler {
  DioException? forwardedError;
  Response<dynamic>? resolvedResponse;

  @override
  void next(DioException error) {
    forwardedError = error;
    super.next(error);
  }

  @override
  void resolve(Response<dynamic> response) {
    resolvedResponse = response;
    super.resolve(response);
  }

  Future<void> waitForCompletion() async {
    try {
      await future;
    } catch (_) {}
  }
}

DioException _unauthorized(RequestOptions options) {
  return DioException(
    requestOptions: options,
    response: Response<dynamic>(requestOptions: options, statusCode: 401),
    type: DioExceptionType.badResponse,
  );
}

void main() {
  setUpAll(() {
    registerFallbackValue(RequestOptions(path: '/'));
  });

  late _MockAuthService auth;
  late _MockDio dio;
  late ExpiredTokenInterceptor interceptor;

  setUp(() {
    auth = _MockAuthService();
    dio = _MockDio();
    interceptor = ExpiredTokenInterceptor(auth, dio);
    when(() => auth.logout()).thenReturn(null);
  });

  group('ExpiredTokenInterceptor', () {
    test(
      'refreshes once and retries the original request with the new token',
      () async {
        when(() => auth.refreshAccessToken()).thenAnswer((_) async => true);
        when(() => auth.token).thenReturn('fresh-token');
        when(() => dio.fetch<dynamic>(any())).thenAnswer(
          (invocation) async => Response<Map<String, dynamic>>(
            requestOptions:
                invocation.positionalArguments.single as RequestOptions,
            statusCode: 200,
            data: {'ok': true},
          ),
        );

        final request = RequestOptions(
          path: '/v1/feed',
          headers: {'Authorization': 'Bearer stale-token'},
        );
        final handler = _RecordingErrorHandler();

        interceptor.onError(_unauthorized(request), handler);
        await handler.waitForCompletion();

        expect(handler.forwardedError, isNull);
        expect(handler.resolvedResponse?.statusCode, 200);
        verify(() => auth.refreshAccessToken()).called(1);
        final retriedRequest =
            verify(() => dio.fetch<dynamic>(captureAny())).captured.single
                as RequestOptions;
        expect(retriedRequest.headers['Authorization'], 'Bearer fresh-token');
        expect(retriedRequest.extra['expired_token_retry'], isTrue);
      },
    );

    test(
      'does not attempt another refresh after a retried request still returns 401',
      () async {
        final request = RequestOptions(
          path: '/v1/feed',
          extra: {'expired_token_retry': true},
        );
        final error = _unauthorized(request);
        final handler = _RecordingErrorHandler();

        interceptor.onError(error, handler);
        await handler.waitForCompletion();

        expect(handler.forwardedError, same(error));
        expect(handler.resolvedResponse, isNull);
        verifyNever(() => auth.refreshAccessToken());
        verifyNever(() => dio.fetch<dynamic>(any()));
      },
    );

    test('logs out and forwards the error when token refresh fails', () async {
      when(() => auth.refreshAccessToken()).thenAnswer((_) async => false);

      final request = RequestOptions(path: '/v1/feed');
      final error = _unauthorized(request);
      final handler = _RecordingErrorHandler();

      interceptor.onError(error, handler);
      await handler.waitForCompletion();

      expect(handler.forwardedError, same(error));
      expect(handler.resolvedResponse, isNull);
      verify(() => auth.refreshAccessToken()).called(1);
      verify(() => auth.logout()).called(1);
      verifyNever(() => dio.fetch<dynamic>(any()));
    });

    test(
      'forwards the retry error if the refreshed request still fails',
      () async {
        when(() => auth.refreshAccessToken()).thenAnswer((_) async => true);
        when(() => auth.token).thenReturn('fresh-token');
        final retryError = DioException(
          requestOptions: RequestOptions(
            path: '/v1/feed',
            extra: {'expired_token_retry': true},
          ),
          response: Response<dynamic>(
            requestOptions: RequestOptions(path: '/v1/feed'),
            statusCode: 403,
          ),
          type: DioExceptionType.badResponse,
        );
        when(() => dio.fetch<dynamic>(any())).thenThrow(retryError);

        final request = RequestOptions(path: '/v1/feed');
        final handler = _RecordingErrorHandler();

        interceptor.onError(_unauthorized(request), handler);
        await handler.waitForCompletion();

        expect(handler.forwardedError, same(retryError));
        expect(handler.resolvedResponse, isNull);
        verify(() => auth.refreshAccessToken()).called(1);
      },
    );
  });
}
