// Generic realtime SSE listener for the AtPost realtime gateway.
//
// Talks to notification-service `/v1/realtime/sse?token=...` (the
// endpoint added in Wave A1). The gateway expects an HMAC-signed
// topic token issued by the domain service (food-service or
// rider-service) via the matching `POST /v1/{food|rider}/realtime/
// token` endpoint.
//
// Lifecycle pattern mirrors notification_stream_service.dart:
//   - utf8.decoder.bind() so multi-byte codepoints survive chunk splits
//   - exponential 1s → 30s backoff with reset on first byte received
//   - 64 KB accumulator cap defends against a runaway server stream
//   - cancellation-aware: caller's stream cancel kills the loop
//
// Wave C1: the Mopedu partner dashboard subscribes here to
// `rider.partner.{partner_id}.offers` so an incoming offer triggers
// the RideRequestModal within ~100ms instead of waiting on the 5s
// poll.

import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/interceptors/auth_interceptor.dart';
import 'package:atpost_app/services/interceptors/expired_token_interceptor.dart';
import 'package:dio/dio.dart';

const _tag = 'RealtimeStreamService';
const _initialBackoff = Duration(seconds: 1);
const _maxBackoff = Duration(seconds: 30);
const _maxBufferBytes = 64 * 1024;

/// One frame emitted by the gateway. `event` is the topic name
/// (e.g. "rider.ride.offered"); `data` is the JSON-decoded payload.
class RealtimeFrame {
  const RealtimeFrame({required this.event, required this.data});

  final String event;
  final Map<String, dynamic> data;
}

/// Owns a long-lived SSE connection scoped to a single topic-token.
/// Construct one per logical subscription (e.g. one per partner
/// listening to their offer topic).
class RealtimeStreamService {
  RealtimeStreamService({
    required AuthService auth,
    required this.token,
    required this.topics,
  }) : _auth = auth {
    _dio = Dio(
      BaseOptions(
        baseUrl: Environment.apiBaseUrl,
        connectTimeout: const Duration(seconds: 30),
      ),
    );
    _dio.interceptors.add(AuthInterceptor(_auth));
    _dio.interceptors.add(ExpiredTokenInterceptor(_auth, _dio));
  }

  final AuthService _auth;

  /// HMAC-signed topic token issued by the domain service.
  final String token;

  /// Topic subset the client wants out of the token's allow-list.
  /// Empty = "give me every topic in the token".
  final List<String> topics;

  late final Dio _dio;
  final StreamController<RealtimeFrame> _controller =
      StreamController<RealtimeFrame>.broadcast();
  CancelToken? _cancelToken;
  bool _running = false;
  bool _stopRequested = false;

  /// Broadcast stream of frames. Multiple listeners share one
  /// underlying SSE connection.
  Stream<RealtimeFrame> get events => _controller.stream;

  /// Boots the read loop. Idempotent.
  void start() {
    if (_running) return;
    _running = true;
    _stopRequested = false;
    unawaited(_loop());
  }

  /// Cancels the active connection. The stream stays open so existing
  /// subscribers don't get a "stream closed" error mid-session.
  void stop() {
    _stopRequested = true;
    _cancelToken?.cancel('realtime stream stopped');
    _cancelToken = null;
    _running = false;
  }

  Future<void> dispose() async {
    stop();
    await _controller.close();
  }

  Future<void> _loop() async {
    var backoff = _initialBackoff;
    while (!_stopRequested) {
      if (_auth.token == null) {
        await Future<void>.delayed(const Duration(milliseconds: 500));
        continue;
      }
      _cancelToken = CancelToken();
      var gotAnyBytes = false;
      try {
        gotAnyBytes = await _readOnce(_cancelToken!);
      } on _CancelledByClient {
        return;
      } catch (e, st) {
        AppLogger.warn(
          'realtime sse loop error',
          tag: _tag,
          error: e,
          stackTrace: st,
        );
      }
      if (_stopRequested) break;
      if (gotAnyBytes) backoff = _initialBackoff;
      await Future<void>.delayed(backoff);
      backoff = Duration(
        milliseconds: math.min(
          _maxBackoff.inMilliseconds,
          (backoff.inMilliseconds * 2).clamp(
            _initialBackoff.inMilliseconds,
            _maxBackoff.inMilliseconds,
          ),
        ),
      );
    }
  }

  Future<bool> _readOnce(CancelToken cancelToken) async {
    final query = <String, String>{'token': token};
    if (topics.isNotEmpty) {
      query['topics'] = topics.join(',');
    }
    final headers = <String, String>{'Accept': 'text/event-stream'};
    Response<ResponseBody>? response;
    try {
      response = await _dio.get<ResponseBody>(
        '/v1/realtime/sse',
        queryParameters: query,
        options: Options(
          responseType: ResponseType.stream,
          headers: headers,
          receiveTimeout: Duration.zero,
        ),
        cancelToken: cancelToken,
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) throw _CancelledByClient();
      rethrow;
    }
    final body = response.data;
    if (body == null) return false;
    final textStream = utf8.decoder.bind(body.stream);
    final buffer = StringBuffer();
    var gotAnyBytes = false;
    try {
      await for (final chunk in textStream) {
        if (_stopRequested) throw _CancelledByClient();
        gotAnyBytes = true;
        buffer.write(chunk);
        if (buffer.length > _maxBufferBytes) {
          AppLogger.warn(
            'realtime sse buffer overflowed; resetting',
            tag: _tag,
          );
          buffer.clear();
          continue;
        }
        // SSE frames are separated by a blank line. Process every
        // complete frame in the buffer.
        while (true) {
          final raw = buffer.toString();
          final terminator = raw.indexOf('\n\n');
          if (terminator < 0) break;
          final frame = raw.substring(0, terminator);
          buffer.clear();
          buffer.write(raw.substring(terminator + 2));
          _dispatchFrame(frame);
        }
      }
    } on _CancelledByClient {
      rethrow;
    } catch (e, st) {
      AppLogger.warn('realtime sse read error', tag: _tag, error: e, stackTrace: st);
    }
    return gotAnyBytes;
  }

  void _dispatchFrame(String frame) {
    String? eventName;
    final dataLines = <String>[];
    for (final line in frame.split('\n')) {
      if (line.startsWith(':')) continue; // comment / keepalive
      if (line.startsWith('event:')) {
        eventName = line.substring(6).trim();
      } else if (line.startsWith('data:')) {
        dataLines.add(line.substring(5).trim());
      }
    }
    if (eventName == null || dataLines.isEmpty) return;
    final body = dataLines.join('\n');
    try {
      final decoded = jsonDecode(body);
      if (decoded is! Map) return;
      // The gateway wraps payloads in an envelope:
      //   { topic, event_type, data, emitted_at }
      // Surface the inner `data` to consumers; fall back to the whole
      // map for events the gateway doesn't envelope (e.g. `connected`).
      final inner = decoded['data'];
      final payload = inner is Map<String, dynamic>
          ? inner
          : Map<String, dynamic>.from(decoded);
      _controller.add(RealtimeFrame(event: eventName, data: payload));
    } catch (e, st) {
      AppLogger.warn(
        'realtime sse decode failed',
        tag: _tag,
        error: e,
        stackTrace: st,
      );
    }
  }
}

class _CancelledByClient implements Exception {}
