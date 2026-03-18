import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Creates a [ProviderContainer] with the given overrides.
///
/// Automatically disposes itself via addTearDown.
ProviderContainer createContainer({
  List<Override> overrides = const [],
}) {
  final container = ProviderContainer(overrides: overrides);
  addTearDown(container.dispose);
  return container;
}

/// Pumps a widget wrapped in [ProviderScope] and [MaterialApp] for testing.
Future<void> pumpApp(
  WidgetTester tester,
  Widget widget, {
  List<Override> overrides = const [],
}) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: overrides,
      child: MaterialApp(
        home: widget,
      ),
    ),
  );
}

/// Creates a mock Dio [Response] with the given data.
Response<dynamic> mockResponse({
  required dynamic data,
  int statusCode = 200,
  String requestPath = '/test',
}) {
  return Response(
    data: data,
    statusCode: statusCode,
    requestOptions: RequestOptions(path: requestPath),
  );
}

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
