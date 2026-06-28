import 'dart:async';
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

  // --- Orphan cleanup ---

  /// Best-effort delete of a media asset whose downstream consumer
  /// (post create, story create, etc.) failed after upload. Audit H7:
  /// without this the row stays at processing_status='uploaded' until
  /// the 24h server-side orphan GC sweep — adequate but slow. Calling
  /// this on the failure path drops storage immediately for the common
  /// case (server returns an error, network blip after confirm). All
  /// errors are swallowed: the goal is opportunistic cleanup, not
  /// reliable deletion (the server sweeper is the reliable path).
  Future<void> tryDeleteMedia(String mediaId) async {
    if (mediaId.isEmpty) return;
    try {
      await _dio.delete('/v1/media/$mediaId');
      AppLogger.info('Cleaned up orphan media $mediaId', tag: _tag);
    } catch (e) {
      AppLogger.info('Orphan media cleanup failed (will rely on server GC): $e',
          tag: _tag);
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
    String step = 'init';
    String? presignedHost;
    int? fileSizeForDiag;
    String? mediaId;
    try {
      final fileData = File(file.path);
      final fileSize = await fileData.length();
      fileSizeForDiag = fileSize;
      final mimeType = lookupMimeType(file.path) ?? 'application/octet-stream';

      // 1. Initialize Upload
      AppLogger.info(
        'Initializing upload for ${file.name} (${fileSize}B, $mimeType)',
        tag: _tag,
      );
      final initRes = await post(
        '/v1/media/init',
        data: {
          'file_type': type,
          'mime_type': mimeType,
          'file_size_bytes': fileSize,
        },
      );

      final initData = initRes.data['data'] as Map<String, dynamic>;
      mediaId = initData['media_id'] as String;
      final uploadUrl = initData['upload_url'] as String;
      presignedHost = Uri.tryParse(uploadUrl)?.host;

      // 2. Perform Physical Upload (using a raw Dio instance to avoid global interceptors for presigned URL)
      step = 'put';
      AppLogger.info(
        'Uploading bytes to presigned URL host=$presignedHost size=${fileSize}B',
        tag: _tag,
      );
      await Dio().put(
        uploadUrl,
        data: fileData.openRead(),
        onSendProgress: onProgress,
        options: Options(
          headers: {'Content-Type': mimeType, 'Content-Length': fileSize},
          // The default 30 s connectTimeout is fine, but receive can
          // run for a while on a real video — let it finish.
          sendTimeout: const Duration(minutes: 5),
          receiveTimeout: const Duration(minutes: 2),
        ),
      );

      // 3. Confirm Upload
      step = 'confirm';
      AppLogger.info('Confirming upload completion', tag: _tag);
      await post('/v1/media/confirm', data: {'media_id': mediaId});

      return mediaId;
    } catch (e, st) {
      // Cleanup orphan media if we initialized it but failed later.
      if (mediaId != null) {
        unawaited(tryDeleteMedia(mediaId));
      }

      // Pull a useful one-liner out of DioException so logcat actually
      // tells us which step blew up + the underlying status / message.
      final detail = e is DioException
          ? 'type=${e.type} status=${e.response?.statusCode} '
                'msg=${e.message} body=${e.response?.data}'
          : e.toString();
      AppLogger.error(
        'Resilient upload failed [step=$step host=$presignedHost size=${fileSizeForDiag ?? -1}B] $detail',
        tag: _tag,
        error: e,
        stackTrace: st,
      );
      throw ErrorHandler.handle(e, st, context: 'ApiClient.uploadMedia.$step');
    }
  }

  /// Files at or above this size use the resumable (chunked) upload path.
  static const int resumableUploadThreshold = 20 * 1024 * 1024; // 20 MB

  /// Resumable multipart upload: init -> upload each part (with per-part
  /// retry) -> complete. A dropped connection costs one 5 MB part's
  /// re-send rather than the whole file. Used for large videos.
  Future<String> uploadMediaResumable(
    XFile file, {
    required String type, // 'image' or 'video'
    void Function(int sent, int total)? onProgress,
  }) async {
    String step = 'init';
    String? mediaId;
    try {
      final fileData = File(file.path);
      final fileSize = await fileData.length();
      final mimeType = lookupMimeType(file.path) ?? 'application/octet-stream';

      // 1. Open the resumable session.
      final initRes = await post(
        '/v1/media/upload/resumable/init',
        data: {
          'file_type': type,
          'mime_type': mimeType,
          'total_bytes': fileSize,
        },
      );
      final initData = initRes.data['data'] as Map<String, dynamic>;
      final uploadId = initData['upload_id'] as String;
      mediaId = initData['media_id'] as String;
      final chunkSize = initData['chunk_size'] as int;
      final totalParts = initData['total_parts'] as int;

      // 2. Upload each part.
      step = 'chunk';
      for (var part = 1; part <= totalParts; part++) {
        final start = (part - 1) * chunkSize;
        final end = (start + chunkSize < fileSize) ? start + chunkSize : fileSize;
        final bytes = <int>[];
        await for (final slice in fileData.openRead(start, end)) {
          bytes.addAll(slice);
        }
        await _uploadPartWithRetry(uploadId, part, bytes);
        onProgress?.call(part, totalParts);
      }

      // 3. Assemble the object + trigger processing.
      step = 'complete';
      await post('/v1/media/upload/resumable/$uploadId/complete');

      return mediaId;
    } catch (e, st) {
      if (mediaId != null) {
        unawaited(tryDeleteMedia(mediaId));
      }
      AppLogger.error(
        'Resumable upload failed [step=$step]',
        tag: _tag,
        error: e,
        stackTrace: st,
      );
      throw ErrorHandler.handle(e, st,
          context: 'ApiClient.uploadMediaResumable.$step');
    }
  }

  Future<void> _uploadPartWithRetry(
    String uploadId,
    int partNumber,
    List<int> bytes, {
    int maxAttempts = 3,
  }) async {
    Object? lastErr;
    for (var attempt = 1; attempt <= maxAttempts; attempt++) {
      try {
        await post(
          '/v1/media/upload/resumable/$uploadId/chunk',
          data: bytes,
          queryParameters: {'part_number': partNumber},
          options: Options(contentType: 'application/octet-stream'),
        );
        return;
      } catch (e) {
        lastErr = e;
        if (attempt < maxAttempts) {
          await Future<void>.delayed(Duration(milliseconds: 500 * attempt));
        }
      }
    }
    throw lastErr!;
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
