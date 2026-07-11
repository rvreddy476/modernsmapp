// Shared social_domain test helpers: mocked ApiClient + canned responses.
import 'package:atpost_network/api_client.dart';
import 'package:dio/dio.dart';
import 'package:mocktail/mocktail.dart';

class MockApiClient extends Mock implements ApiClient {}

/// A 200 response carrying [body] as-is (tests pass the full
/// `{'data': ...}` envelope the services return).
Response<dynamic> ok(Object? body, {String path = '/'}) => Response(
      requestOptions: RequestOptions(path: path),
      statusCode: 200,
      data: body,
    );

/// A DioException with [status], for exercising fallback paths.
DioException httpError(int status, {String path = '/'}) => DioException(
      requestOptions: RequestOptions(path: path),
      response: Response(
        requestOptions: RequestOptions(path: path),
        statusCode: status,
      ),
      type: DioExceptionType.badResponse,
    );
