import 'dart:io';

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/services/pulse_auth_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

// formerly PostMatchApiClient
class PulseApiClient {
  final Dio _dio;
  final PulseAuthService _auth;

  PulseApiClient(this._auth)
    : _dio = Dio(
        BaseOptions(
          baseUrl: Environment.pulseBaseUrl,
          connectTimeout: const Duration(seconds: 20),
          receiveTimeout: const Duration(seconds: 20),
          headers: const {
            'Content-Type': 'application/json',
            'Accept': 'application/json',
          },
        ),
      ) {
    _dio.interceptors.add(
      QueuedInterceptorsWrapper(
        onRequest: (options, handler) {
          final token = _auth.accessToken;
          if (token != null && token.isNotEmpty) {
            options.headers['Authorization'] = 'Bearer $token';
          }
          handler.next(options);
        },
        onError: (error, handler) async {
          final shouldRetry =
              error.response?.statusCode == 401 &&
              error.requestOptions.extra['pulse_retry'] != true &&
              _auth.refreshToken != null;
          if (!shouldRetry) {
            handler.next(error);
            return;
          }

          final refreshed = await _auth.refreshAccessToken();
          if (!refreshed) {
            handler.next(error);
            return;
          }

          final requestOptions = error.requestOptions;
          requestOptions.headers['Authorization'] =
              'Bearer ${_auth.accessToken}';
          requestOptions.extra['pulse_retry'] = true;

          try {
            final response = await _dio.fetch<dynamic>(requestOptions);
            handler.resolve(response);
          } on DioException catch (dioError) {
            handler.next(dioError);
          }
        },
      ),
    );
  }

  Future<Response<T>> get<T>(
    String path, {
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) {
    return _dio.get<T>(
      path,
      queryParameters: queryParameters,
      options: options,
    );
  }

  Future<Response<T>> post<T>(
    String path, {
    Object? data,
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) {
    return _dio.post<T>(
      path,
      data: data,
      queryParameters: queryParameters,
      options: options,
    );
  }

  Future<Response<T>> put<T>(String path, {Object? data, Options? options}) {
    return _dio.put<T>(path, data: data, options: options);
  }

  Future<Response<T>> patch<T>(String path, {Object? data, Options? options}) {
    return _dio.patch<T>(path, data: data, options: options);
  }

  Future<Response<T>> delete<T>(String path, {Object? data, Options? options}) {
    return _dio.delete<T>(path, data: data, options: options);
  }

  Future<void> uploadToPresignedUrl({
    required String uploadUrl,
    required File file,
    required String contentType,
    ProgressCallback? onSendProgress,
  }) async {
    final length = await file.length();
    await Dio().put(
      uploadUrl,
      data: file.openRead(),
      onSendProgress: onSendProgress,
      options: Options(
        headers: {'Content-Type': contentType, 'Content-Length': length},
      ),
    );
  }
}

final pulseApiClientProvider = Provider<PulseApiClient>((ref) {
  return PulseApiClient(ref.watch(pulseAuthServiceProvider));
});
