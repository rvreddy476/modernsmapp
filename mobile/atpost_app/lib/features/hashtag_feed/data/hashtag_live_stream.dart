// Minimal SSE client for the hashtag real-time streams. Two public
// entry points:
//   - subscribe(tag)      → /v1/hashtags/<tag>/stream, emits one
//                            HashtagStreamEvent per `new_post` frame
//   - subscribeTrending() → /v1/hashtags/trending/stream, emits one
//                            HashtagSnapshot per `trending` frame
//
// Not a general-purpose SSE library — only the subset post-service
// emits: `event: <name>\ndata: <json>\n\n`, optionally preceded by
// `:` keepalive comments.
//
// Resilience:
// - UTF-8 is decoded via utf8.decoder.bind() so codepoints split
//   across TCP chunks (emoji, CJK, Hindi, etc.) are handled
//   correctly. The earlier naive `utf8.decode(chunk)` would corrupt
//   those.
// - The connection auto-reconnects with exponential backoff (1s → 30s
//   cap) when the underlying stream drops for any non-cancel reason.
//   Cancellation by the caller stops the loop cleanly.
// - The accumulator buffer is hard-capped at _maxBufferBytes to
//   protect against a malformed server stream that never delimits
//   events.

import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/features/hashtag_feed/models/hashtag_model.dart';
import 'package:dio/dio.dart';

const _tag = 'HashtagLiveStream';
const _initialBackoff = Duration(seconds: 1);
const _maxBackoff = Duration(seconds: 30);
// Hard ceiling on the accumulator buffer. Real SSE events from
// post-service are <2 KB; 64 KB leaves plenty of slack while
// guaranteeing a runaway server can't blow up the device.
const _maxBufferBytes = 64 * 1024;

/// One decoded `new_post` event off the per-tag stream. We don't
/// bind to a strict shape so the SSE payload can grow without
/// forcing a client rebuild — the UI only cares "something new
/// happened".
class HashtagStreamEvent {
  const HashtagStreamEvent(this.payload);
  final Map<String, dynamic> payload;
}

/// One decoded `trending` snapshot off the global stream. The
/// publisher debounces to ~30 s so each emission represents a real
/// change in the top-N (membership, rank, or post count).
class HashtagSnapshot {
  const HashtagSnapshot({required this.tags, required this.updatedAt});

  final List<HashtagModel> tags;
  final DateTime updatedAt;
}

class HashtagLiveStream {
  HashtagLiveStream({Dio? dio}) : _dio = dio ?? Dio();

  final Dio _dio;

  Stream<HashtagStreamEvent> subscribe(String tag) {
    final url = '${Environment.apiBaseUrl}/v1/hashtags/$tag/stream';
    return _openWithReconnect<HashtagStreamEvent>(
      url: url,
      acceptedEventName: 'new_post',
      mapper: (data) => HashtagStreamEvent(data),
    );
  }

  /// Opens GET /v1/hashtags/trending/stream and emits one
  /// HashtagSnapshot per `trending` event. The publisher in
  /// post-service is leader-elected + debounced so this stream is
  /// quiet by design — typically 0–2 messages/min cluster-wide
  /// regardless of how many clients are subscribed.
  Stream<HashtagSnapshot> subscribeTrending() {
    final url = '${Environment.apiBaseUrl}/v1/hashtags/trending/stream';
    return _openWithReconnect<HashtagSnapshot>(
      url: url,
      acceptedEventName: 'trending',
      mapper: _decodeTrending,
    );
  }

  HashtagSnapshot _decodeTrending(Map<String, dynamic> data) {
    final rawTags = (data['hashtags'] as List?) ?? const [];
    final tags = <HashtagModel>[];
    for (final raw in rawTags) {
      if (raw is Map<String, dynamic>) {
        tags.add(HashtagModel.fromJson(raw));
      } else if (raw is Map) {
        tags.add(HashtagModel.fromJson(Map<String, dynamic>.from(raw)));
      }
    }
    final updatedRaw = data['updated_at'];
    final updatedAt = updatedRaw is String
        ? DateTime.tryParse(updatedRaw)?.toLocal() ?? DateTime.now()
        : DateTime.now();
    return HashtagSnapshot(tags: tags, updatedAt: updatedAt);
  }

  /// Generic resilient SSE loop. Owns one StreamController and one
  /// "current attempt" CancelToken. On any non-cancel drop, waits
  /// `backoff` then retries — backoff doubles each attempt up to
  /// _maxBackoff. Successfully receiving any byte from the server
  /// resets the backoff to _initialBackoff so a long-running connection
  /// that finally dies doesn't spend 30 s offline before its first
  /// retry.
  Stream<T> _openWithReconnect<T>({
    required String url,
    required String acceptedEventName,
    required T Function(Map<String, dynamic> data) mapper,
  }) {
    late StreamController<T> controller;
    CancelToken? activeToken;
    var closed = false;

    Future<void> loop() async {
      var backoff = _initialBackoff;
      while (!closed) {
        activeToken = CancelToken();
        final receivedBytes = _AttemptResult();
        try {
          await _readOnce(
            url: url,
            acceptedEventName: acceptedEventName,
            mapper: mapper,
            controller: controller,
            cancelToken: activeToken!,
            attempt: receivedBytes,
          );
          // Stream ended cleanly from the server (no error). Reconnect
          // after a short pause unless the caller cancelled.
        } on _CancelledByClient {
          return;
        } catch (e, st) {
          AppLogger.warn(
            'sse loop error ($acceptedEventName)',
            tag: _tag,
            error: e,
            stackTrace: st,
          );
        }
        if (closed) break;
        // Reset backoff if we made it past the headers AND saw any
        // data; otherwise keep ramping up (server may be 404'ing).
        if (receivedBytes.gotAnyBytes) backoff = _initialBackoff;
        await Future<void>.delayed(backoff);
        backoff = Duration(
          milliseconds: math.min(
            _maxBackoff.inMilliseconds,
            (backoff.inMilliseconds * 2).clamp(_initialBackoff.inMilliseconds, _maxBackoff.inMilliseconds),
          ),
        );
      }
      if (!controller.isClosed) await controller.close();
    }

    controller = StreamController<T>(
      onCancel: () {
        closed = true;
        activeToken?.cancel('stream closed by client');
      },
    );
    // Fire-and-forget — the loop owns its own lifecycle.
    unawaited(loop());
    return controller.stream;
  }

  Future<void> _readOnce<T>({
    required String url,
    required String acceptedEventName,
    required T Function(Map<String, dynamic> data) mapper,
    required StreamController<T> controller,
    required CancelToken cancelToken,
    required _AttemptResult attempt,
  }) async {
    Response<ResponseBody>? response;
    try {
      response = await _dio.get<ResponseBody>(
        url,
        options: Options(
          responseType: ResponseType.stream,
          headers: {'Accept': 'text/event-stream'},
          // SSE connections are long-lived — disable the global
          // receive timeout so a quiet stream isn't killed.
          receiveTimeout: Duration.zero,
        ),
        cancelToken: cancelToken,
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) {
        throw _CancelledByClient();
      }
      rethrow;
    }

    final body = response.data;
    if (body == null) return;

    // utf8.decoder.bind() handles multi-byte codepoints split across
    // chunk boundaries — critical for streams that carry hashtags or
    // post snippets in non-ASCII scripts.
    final textStream = utf8.decoder.bind(body.stream);
    final buffer = StringBuffer();

    try {
      await for (final chunk in textStream) {
        if (controller.isClosed) throw _CancelledByClient();
        attempt.gotAnyBytes = true;
        buffer.write(chunk);
        if (buffer.length > _maxBufferBytes) {
          AppLogger.warn(
            'sse buffer overflowed without event delimiter; resetting',
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
          final data = _parsePayload(raw, acceptedEventName);
          if (data != null) controller.add(mapper(data));
        }
        buffer.clear();
        buffer.write(combined);
      }
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) throw _CancelledByClient();
      rethrow;
    }
  }

  /// Parses one SSE event block. Returns the decoded JSON payload when
  /// the block names [acceptedEventName] and has a valid `data:` line,
  /// otherwise null. Keepalive comments (`:`-prefixed lines) and other
  /// event names (`connected`, `keepalive`, …) drop through to null
  /// silently.
  Map<String, dynamic>? _parsePayload(String raw, String acceptedEventName) {
    String? event;
    final dataLines = <String>[];
    for (final line in raw.split('\n')) {
      if (line.isEmpty) continue;
      if (line.startsWith(':')) continue; // keepalive comment
      if (line.startsWith('event:')) {
        event = line.substring(6).trim();
      } else if (line.startsWith('data:')) {
        dataLines.add(line.substring(5).trim());
      }
    }
    if (event != acceptedEventName || dataLines.isEmpty) return null;
    try {
      final decoded = jsonDecode(dataLines.join('\n'));
      if (decoded is Map<String, dynamic>) return decoded;
      if (decoded is Map) return Map<String, dynamic>.from(decoded);
      return null;
    } catch (_) {
      return null;
    }
  }
}

/// Internal sentinel — only used to short-circuit the retry loop when
/// the caller explicitly cancelled the subscription.
class _CancelledByClient implements Exception {}

/// Tiny mutable holder for the inner-loop signal "we got *something*
/// from the server before failing", used to reset the backoff.
class _AttemptResult {
  bool gotAnyBytes = false;
}
