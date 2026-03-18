import 'package:atpost_app/core/errors/app_exception.dart';
import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

import '../../helpers/test_utils.dart';

void main() {
  group('AppException hierarchy', () {
    test('NetworkException has correct userMessage', () {
      const e = NetworkException(message: 'timeout');
      expect(e.userMessage, contains('internet connection'));
      expect(e.statusCode, isNull);
    });

    test('ServerException has correct userMessage', () {
      const e = ServerException(message: 'internal error', statusCode: 500);
      expect(e.userMessage, contains('Something went wrong'));
      expect(e.statusCode, 500);
    });

    test('AuthException distinguishes 401 vs 403', () {
      const e401 = AuthException(message: 'unauthorized', statusCode: 401);
      expect(e401.userMessage, contains('session has expired'));

      const e403 = AuthException(message: 'forbidden', statusCode: 403);
      expect(e403.userMessage, contains('permission'));
    });

    test('NotFoundException has correct defaults', () {
      const e = NotFoundException(message: 'not found');
      expect(e.statusCode, 404);
      expect(e.userMessage, contains('not found'));
    });

    test('ValidationException shows field errors when available', () {
      const e = ValidationException(
        message: 'validation failed',
        fieldErrors: {'email': 'Invalid email address'},
      );
      expect(e.userMessage, 'Invalid email address');

      const e2 = ValidationException(message: 'validation failed');
      expect(e2.userMessage, contains('check your input'));
    });
  });

  group('ErrorHandler', () {
    test('converts DioException timeout to NetworkException', () {
      final dio = mockDioError(type: DioExceptionType.connectionTimeout);
      final result = ErrorHandler.handle(dio, StackTrace.current);
      expect(result, isA<NetworkException>());
    });

    test('converts DioException 404 to NotFoundException', () {
      final dio = mockDioError(statusCode: 404, data: {'message': 'Not found'});
      final result = ErrorHandler.handle(dio, StackTrace.current);
      expect(result, isA<NotFoundException>());
    });

    test('converts DioException 500 to ServerException', () {
      final dio = mockDioError(statusCode: 500, data: {'message': 'Internal error'});
      final result = ErrorHandler.handle(dio, StackTrace.current);
      expect(result, isA<ServerException>());
      expect(result.statusCode, 500);
    });

    test('converts DioException 401 to AuthException', () {
      final dio = mockDioError(statusCode: 401, data: {'message': 'Unauthorized'});
      final result = ErrorHandler.handle(dio, StackTrace.current);
      expect(result, isA<AuthException>());
    });

    test('passes through existing AppException unchanged', () {
      const original = NetworkException(message: 'test');
      final result = ErrorHandler.handle(original, StackTrace.current);
      expect(result, isA<NetworkException>());
      expect(result.message, 'test');
    });
  });
}
