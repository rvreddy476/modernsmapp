
import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/errors/app_exception.dart';
import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/interceptors/auth_interceptor.dart';
import 'package:atpost_app/services/interceptors/csrf_interceptor.dart';
import 'package:atpost_app/services/interceptors/expired_token_interceptor.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

/// Production-ready API client with auth, CSRF, and token refresh logic.
class ApiClient {
  final Dio _dio;
  final AuthService _auth;
  static const _tag = 'ApiClient';

  ApiClient(this._auth)
      : _dio = Dio(BaseOptions(
          baseUrl: Environment.apiBaseUrl,
          connectTimeout: const Duration(seconds: 15),
          receiveTimeout: const Duration(seconds: 20),
          headers: {
            'Content-Type': 'application/json',
            'Accept': 'application/json',
          },
        )) {
    // Interceptor order is important:
    // 1. Auth (injects tokens)
    // 2. CSRF (injects CSRF tokens)
    // 3. ExpiredToken (handles 401s by refreshing and retrying)
    _dio.interceptors.addAll([
      AuthInterceptor(_auth),
      CsrfInterceptor(),
      ExpiredTokenInterceptor(_auth, _dio),
      // Add a simple logging interceptor for debug mode
      LogInterceptor(
        requestHeader: true,
        requestBody: true,
        responseHeader: true,
        responseBody: false, // Don't log full response bodies to avoid clutter
        error: true,
        logPrint: (obj) => AppLogger.debug(obj.toString(), tag: 'Network'),
      ),
    ]);
  }

  /// Perform a GET request.
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

  /// Perform a POST request.
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

  /// Perform a PUT request.
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

  /// Perform a PATCH request.
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

  /// Perform a DELETE request.
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

  /// Upload a file with progress tracking.
  Future<String> uploadMedia(
    XFile file, {
    required String type,
    void Function(int sent, int total)? onProgress,
  }) async {
    try {
      AppLogger.info('Uploading $type file: ${file.name}', tag: _tag);

      final formData = FormData.fromMap({
        'file': await MultipartFile.fromFile(
          file.path,
          filename: file.name,
        ),
        'type': type,
      });

      final response = await _dio.post(
        '${Environment.mediaPath}/upload',
        data: formData,
        onSendProgress: onProgress,
        options: Options(
          contentType: 'multipart/form-data',
          // Uploads usually take longer
          sendTimeout: const Duration(minutes: 5),
          receiveTimeout: const Duration(minutes: 5),
        ),
      );

      final data = response.data;
      if (data is Map<String, dynamic>) {
        final payload = data['data'] as Map<String, dynamic>? ?? data;
        final mediaId = (payload['media_id'] ?? payload['id'] ?? '') as String;
        AppLogger.info('Upload successful. Media ID: $mediaId', tag: _tag);
        return mediaId;
      }
      return '';
    } catch (e) {
      AppLogger.error('Media upload failed', tag: _tag, error: e);
      rethrow;
    }
  }

  /// Perform a DELETE request with a JSON body.
  /// Alias for [delete] — provided for clarity when passing request data.
  Future<Response<T>> deleteWithData<T>(
    String path, {
    Object? data,
    Options? options,
    CancelToken? cancelToken,
  }) {
    return delete<T>(path, data: data, options: options, cancelToken: cancelToken);
  }

  /// Centralized error handling: converts raw errors to typed [AppException]
  /// and logs them via [ErrorHandler].
  Never _handleError(Object error, String path) {
    final st = error is DioException ? error.stackTrace : StackTrace.current;
    final appException = ErrorHandler.handle(error, st, context: 'ApiClient.$path');
    throw appException;
  }
}

/// Global API client provider.
final apiClientProvider = Provider<ApiClient>((ref) {
  final auth = ref.watch(authServiceProvider);
  return ApiClient(auth);
});
