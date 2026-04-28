import 'dart:io';
import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/interceptors/auth_interceptor.dart';
import 'package:atpost_app/services/interceptors/csrf_interceptor.dart';
import 'package:atpost_app/services/interceptors/expired_token_interceptor.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';
import 'package:mime/mime.dart';

/// Production-grade API client synchronized with OpenAPI spec.
/// Supports 3-step resilient media uploads and automated token refreshing.
class ApiClient {
  final Dio _dio;
  final AuthService _auth;
  static const _tag = 'ApiClient';

  ApiClient(this._auth)
    : _dio = Dio(
        BaseOptions(
          baseUrl: Environment.apiBaseUrl,
          connectTimeout: const Duration(seconds: 30),
          receiveTimeout: const Duration(seconds: 30),
          headers: {
            'Content-Type': 'application/json',
            'Accept': 'application/json',
          },
        ),
      ) {
    AppLogger.info(
      'Configured API base URL: ${Environment.apiBaseUrl}',
      tag: _tag,
    );
    _dio.interceptors.addAll([
      AuthInterceptor(_auth),
      CsrfInterceptor(),
      ExpiredTokenInterceptor(_auth, _dio),
      LogInterceptor(
        requestHeader: true,
        requestBody: true,
        responseHeader: true,
        responseBody: false,
        error: true,
        logPrint: (obj) => AppLogger.debug(obj.toString(), tag: 'Network'),
      ),
    ]);
  }

  // --- Core Methods ---

  Future<Response<T>> get<T>(
    String path, {
    Map<String, dynamic>? queryParameters,
    Options? options,
    CancelToken? cancelToken,
  }) async {
    try {
      return await _dio.get<T>(
        path,
        queryParameters: queryParameters,
        options: options,
        cancelToken: cancelToken,
      );
    } catch (e) {
      _handleError(e, path);
    }
  }

  Future<Response<T>> post<T>(
    String path, {
    Object? data,
    Map<String, dynamic>? queryParameters,
    Options? options,
    CancelToken? cancelToken,
  }) async {
    try {
      return await _dio.post<T>(
        path,
        data: data,
        queryParameters: queryParameters,
        options: options,
        cancelToken: cancelToken,
      );
    } catch (e) {
      _handleError(e, path);
    }
  }

  Future<Response<T>> put<T>(
    String path, {
    Object? data,
    Options? options,
    CancelToken? cancelToken,
  }) async {
    try {
      return await _dio.put<T>(
        path,
        data: data,
        options: options,
        cancelToken: cancelToken,
      );
    } catch (e) {
      _handleError(e, path);
    }
  }

  Future<Response<T>> patch<T>(
    String path, {
    Object? data,
    Options? options,
    CancelToken? cancelToken,
  }) async {
    try {
      return await _dio.patch<T>(
        path,
        data: data,
        options: options,
        cancelToken: cancelToken,
      );
    } catch (e) {
      _handleError(e, path);
    }
  }

  Future<Response<T>> delete<T>(
    String path, {
    Object? data,
    Options? options,
    CancelToken? cancelToken,
  }) async {
    try {
      return await _dio.delete<T>(
        path,
        data: data,
        options: options,
        cancelToken: cancelToken,
      );
    } catch (e) {
      _handleError(e, path);
    }
  }

  // --- Production Scale Media Upload (3-Step Spec) ---

  /// Orchestrates a resilient 3-step media upload as per OpenAPI spec:
  /// 1. Initialize (/v1/media/init)
  /// 2. Upload to Presigned URL
  /// 3. Confirm (/v1/media/confirm)
  Future<String> uploadMedia(
    XFile file, {
    required String type, // 'image' or 'video'
    void Function(int sent, int total)? onProgress,
  }) async {
    try {
      final fileData = File(file.path);
      final fileSize = await fileData.length();
      final mimeType = lookupMimeType(file.path) ?? 'application/octet-stream';

      // 1. Initialize Upload
      AppLogger.info('Initializing upload for ${file.name}', tag: _tag);
      final initRes = await post(
        '/v1/media/init',
        data: {
          'file_type': type,
          'mime_type': mimeType,
          'file_size_bytes': fileSize,
        },
      );

      final initData = initRes.data['data'] as Map<String, dynamic>;
      final mediaId = initData['media_id'] as String;
      final uploadUrl = initData['upload_url'] as String;

      // 2. Perform Physical Upload (using a raw Dio instance to avoid global interceptors for presigned URL)
      AppLogger.info('Uploading bytes to presigned URL', tag: _tag);
      await Dio().put(
        uploadUrl,
        data: fileData.openRead(),
        onSendProgress: onProgress,
        options: Options(
          headers: {'Content-Type': mimeType, 'Content-Length': fileSize},
        ),
      );

      // 3. Confirm Upload
      AppLogger.info('Confirming upload completion', tag: _tag);
      await post('/v1/media/confirm', data: {'media_id': mediaId});

      return mediaId;
    } catch (e, st) {
      AppLogger.error(
        'Resilient upload failed',
        tag: _tag,
        error: e,
        stackTrace: st,
      );
      throw ErrorHandler.handle(e, st, context: 'ApiClient.uploadMedia');
    }
  }

  Never _handleError(Object error, String path) {
    final st = error is DioException ? error.stackTrace : StackTrace.current;
    final appException = ErrorHandler.handle(
      error,
      st,
      context: 'ApiClient.$path',
    );
    throw appException;
  }
}

final apiClientProvider = Provider<ApiClient>((ref) {
  final auth = ref.watch(authServiceProvider);
  return ApiClient(auth);
});
