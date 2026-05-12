// Dedicated SSE client for the notification stream.
//
// Why a separate service instead of riding the existing WS multiplex:
// README §1 + §17 prescribes SSE for notifications and WS only for
// chat. SSE buys us:
//   - native Last-Event-ID replay (the WS path doesn't carry the
//     header and ws-gateway has no cursor concept)
//   - independent scaling — chat-ws-gateway no longer needs to fan
//     notifications, freeing it to specialize on chat protocol work
//   - a clean retry/backoff lifecycle without entangling presence,
//     feed, and chat reconnect semantics
//
// The mobile WS multiplex still emits `NotificationEvent`s as a side
// effect of relaying `notify:<uid>`, but the live consumer
// (liveNotificationsProvider) sources from THIS stream instead, and
// the dispatcher in realtime_event.dart routes `notification` over
// here. No dedupe needed because consumers only listen to one source.
//
// Resilience pattern matches features/hashtag_feed/data/hashtag_live_stream.dart:
//   - utf8.decoder.bind() so multi-byte codepoints survive chunk splits
//   - exponential 1 s → 30 s backoff with reset on first byte received
//   - hard cap on the accumulator buffer (64 KB) to defend against
//     a malformed/runaway server stream
//   - cancellation-aware: caller's stream cancel kills the loop

import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/interceptors/auth_interceptor.dart';
import 'package:atpost_app/services/interceptors/expired_token_interceptor.dart';
import 'package:dio/dio.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

const _tag = 'NotificationStreamService';
const _initialBackoff = Duration(seconds: 1);
const _maxBackoff = Duration(seconds: 30);
const _maxBufferBytes = 64 * 1024;
const _lastEventIdKey = 'notif.sse.last_event_id';

/// Owns the long-lived SSE connection and a broadcast stream of
/// NotificationEvent. Created once per authed session.
class NotificationStreamService {
  NotificationStreamService(this._auth, {FlutterSecureStorage? storage})
      : _storage = storage ?? const FlutterSecureStorage() {
    _dio = Dio(
      BaseOptions(
        baseUrl: Environment.apiBaseUrl,
        // Don't send the default JSON Accept — set per-request.
        connectTimeout: const Duration(seconds: 30),
      ),
    );
    _dio.interceptors.add(AuthInterceptor(_auth));
    // ExpiredTokenInterceptor handles 401 refresh + retry. We use the
    // same instance as the global API client because the refresh
    // flow lives on it.
    _dio.interceptors.add(ExpiredTokenInterceptor(_auth, _dio));
  }

  final AuthService _auth;
  final FlutterSecureStorage _storage;
  late final Dio _dio;

  final StreamController<NotificationEvent> _controller =
      StreamController<NotificationEvent>.broadcast();

  CancelToken? _cancelToken;
  bool _running = false;
  bool _stopRequested = false;

  /// Broadcast stream of NotificationEvents. Safe to listen multiple
  /// times — closing one subscription doesn't tear down the SSE
  /// connection.
  Stream<NotificationEvent> get events => _controller.stream;

  /// Boots the read loop. Idempotent — calling twice is a no-op.
  void start() {
    if (_running) return;
    _running = true;
    _stopRequested = false;
    unawaited(_loop());
  }

  /// Cancels the active connection and stops reconnect attempts.
  /// The stream stays open so existing subscribers don't get a
  /// "stream closed" error mid-session.
  void stop() {
    _stopRequested = true;
    _cancelToken?.cancel('notification stream stopped');
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
        // Wait for the session to come up. Polling is cheap; the
        // session usually lands within milliseconds of app start.
        await Future<void>.delayed(const Duration(milliseconds: 500));
        continue;
      }
      _cancelToken = CancelToken();
      final attempt = _AttemptResult();
      try {
        await _readOnce(_cancelToken!, attempt);
      } on _CancelledByClient {
        return;
      } catch (e, st) {
        AppLogger.warn(
          'notification sse loop error',
          tag: _tag,
          error: e,
          stackTrace: st,
        );
      }
      if (_stopRequested) break;
      if (attempt.gotAnyBytes) backoff = _initialBackoff;
      await Future<void>.delayed(backoff);
      backoff = Duration(
        milliseconds: math.min(
          _maxBackoff.inMilliseconds,
          (backoff.inMilliseconds * 2)
              .clamp(_initialBackoff.inMilliseconds, _maxBackoff.inMilliseconds),
        ),
      );
    }
  }

  Future<void> _readOnce(
    CancelToken cancelToken,
    _AttemptResult attempt,
  ) async {
    final lastId = await _storage.read(key: _lastEventIdKey);
    final headers = <String, String>{'Accept': 'text/event-stream'};
    if (lastId != null && lastId.isNotEmpty) {
      headers['Last-Event-ID'] = lastId;
    }

    Response<ResponseBody>? response;
    try {
      response = await _dio.get<ResponseBody>(
        '/v1/notifications/stream',
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
    if (body == null) return;

    final textStream = utf8.decoder.bind(body.stream);
    final buffer = StringBuffer();
    try {
      await for (final chunk in textStream) {
        if (_stopRequested) throw _CancelledByClient();
        attempt.gotAnyBytes = true;
        buffer.write(chunk);
        if (buffer.length > _maxBufferBytes) {
          AppLogger.warn(
            'notification sse buffer overflowed; resetting',
            tag: _tag,
          );
          buffer.clear();
          continue;
        }
        var combined = buffer.toString();
        while (true) {
          final boundary = combined.indexOf('\n\n');
          if (boundary < 0) break;
          final raw = combined.substring(0, boundary);
          combined = combined.substring(boundary + 2);
          await _handleEvent(raw);
        }
        buffer.clear();
        buffer.write(combined);
      }
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) throw _CancelledByClient();
      rethrow;
    }
  }

  Future<void> _handleEvent(String raw) async {
    String? id;
    String? event;
    final dataLines = <String>[];
    for (final line in raw.split('\n')) {
      if (line.isEmpty || line.startsWith(':')) continue;
      if (line.startsWith('id:')) {
        id = line.substring(3).trim();
      } else if (line.startsWith('event:')) {
        event = line.substring(6).trim();
      } else if (line.startsWith('data:')) {
        dataLines.add(line.substring(5).trim());
      }
    }
    if (event != 'notification' || dataLines.isEmpty) return;

    try {
      final decoded = jsonDecode(dataLines.join('\n'));
      Map<String, dynamic> envelope;
      if (decoded is Map<String, dynamic>) {
        envelope = decoded;
      } else if (decoded is Map) {
        envelope = Map<String, dynamic>.from(decoded);
      } else {
        return;
      }
      // processor.go emits `{ "type": "notification", "payload": {...} }`.
      // RealtimeEvent.fromJson reads `type` + `payload`, so we hand
      // the whole envelope to keep the dispatcher in one place.
      final realtime = RealtimeEvent.fromJson(envelope);
      if (realtime is NotificationEvent && !_controller.isClosed) {
        _controller.add(realtime);
        // Persist the SSE event id (when present) so the next
        // reconnect — including after force-quit — replays only what
        // we actually missed.
        if (id != null && id.isNotEmpty) {
          await _storage.write(key: _lastEventIdKey, value: id);
        }
      }
    } catch (e) {
      AppLogger.warn(
        'notification sse event decode failed',
        tag: _tag,
        error: e,
      );
    }
  }
}

class _CancelledByClient implements Exception {}

class _AttemptResult {
  bool gotAnyBytes = false;
}
