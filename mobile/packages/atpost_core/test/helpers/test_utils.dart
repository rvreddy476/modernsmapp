import 'package:dio/dio.dart';

/// Creates a [DioException] for testing error scenarios.
DioException mockDioError({
  int? statusCode,
  dynamic data,
  DioExceptionType type = DioExceptionType.badResponse,
  String? message,
}) {
  final requestOptions = RequestOptions(path: '/test');
  return DioException(
    requestOptions: requestOptions,
    response: statusCode != null
        ? Response(
            statusCode: statusCode,
            data: data,
            requestOptions: requestOptions,
          )
        : null,
    type: type,
    message: message,
  );
}
