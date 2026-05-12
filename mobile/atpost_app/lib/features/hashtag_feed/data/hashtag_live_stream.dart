// Minimal SSE client for the hashtag real-time streams. Two public
// entry points:
//   - subscribe(tag)      → /v1/hashtags/<tag>/stream, emits one
//                            HashtagStreamEvent per `new_post` frame
//   - subscribeTrending() → /v1/hashtags/trending/stream, emits one
//                            HashtagSnapshot per `trending` frame
//
// Not a general-purpose SSE library — only the subset post-service
// emits: `event: <name>\ndata: <json>\n\n`, optionally preceded by
// `:` keepalive comments. That's enough for the per-tag pill + the
// trending chip strip.

import 'dart:async';
import 'dart:convert';

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/features/hashtag_feed/models/hashtag_model.dart';
import 'package:dio/dio.dart';

/// One decoded `new_post` event off the per-tag stream. We don't
/// bind to a strict shape so the SSE payload can grow without
/// forcing a client rebuild — the UI only cares "something new
/// happened".
class HashtagStreamEvent {
  const HashtagStreamEvent(this.payload);

  /// Decoded JSON body of the `data:` line. Empty map on parse failure
  /// (e.g. heartbeat — though those are filtered above).
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

  /// Opens a streaming GET to `/v1/hashtags/<tag>/stream` and emits one
  /// HashtagStreamEvent per `event: new_post` line group. Caller
  /// subscribes via the returned StreamSubscription and cancels it
  /// when the screen disposes.
  ///
  /// The HTTP connection auto-closes when the subscription is
  /// cancelled. Transient network errors propagate through onError;
  /// callers should usually log + ignore them since the user can
  /// pull-to-refresh manually.
  Stream<HashtagStreamEvent> subscribe(String tag) {
    final url = '${Environment.apiBaseUrl}/v1/hashtags/$tag/stream';
    final controller = StreamController<HashtagStreamEvent>();
    final cancelToken = CancelToken();

    controller.onCancel = () {
      cancelToken.cancel('stream closed by client');
    };

    _open(url, 'new_post', cancelToken, controller,
        (data) => HashtagStreamEvent(data));
    return controller.stream;
  }

  /// Opens GET /v1/hashtags/trending/stream and emits one
  /// HashtagSnapshot per `trending` event. The publisher in
  /// post-service is leader-elected + debounced so this stream is
  /// quiet by design — typically 0–2 messages/min cluster-wide
  /// regardless of how many clients are subscribed.
  Stream<HashtagSnapshot> subscribeTrending() {
    final url = '${Environment.apiBaseUrl}/v1/hashtags/trending/stream';
    final controller = StreamController<HashtagSnapshot>();
    final cancelToken = CancelToken();
    controller.onCancel = () => cancelToken.cancel('stream closed by client');
    _open(url, 'trending', cancelToken, controller, _decodeTrending);
    return controller.stream;
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

  Future<void> _open<T>(
    String url,
    String acceptedEventName,
    CancelToken cancelToken,
    StreamController<T> controller,
    T Function(Map<String, dynamic> data) mapper,
  ) async {
    try {
      final response = await _dio.get<ResponseBody>(
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

      final body = response.data;
      if (body == null) {
        await controller.close();
        return;
      }

      var buffer = '';
      await for (final chunk in body.stream) {
        if (controller.isClosed) return;
        buffer += utf8.decode(chunk, allowMalformed: true);
        // SSE events are separated by blank lines (`\n\n`). Split,
        // keeping any trailing partial event in `buffer`.
        while (true) {
          final boundary = buffer.indexOf('\n\n');
          if (boundary < 0) break;
          final raw = buffer.substring(0, boundary);
          buffer = buffer.substring(boundary + 2);
          final data = _parsePayload(raw, acceptedEventName);
          if (data != null) controller.add(mapper(data));
        }
      }
      await controller.close();
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) {
        await controller.close();
        return;
      }
      AppLogger.warn(
        'hashtag stream error',
        tag: 'HashtagLiveStream',
        error: e,
      );
      controller.addError(e);
      await controller.close();
    } catch (e, st) {
      AppLogger.warn(
        'hashtag stream unexpected error',
        tag: 'HashtagLiveStream',
        error: e,
        stackTrace: st,
      );
      controller.addError(e);
      await controller.close();
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
