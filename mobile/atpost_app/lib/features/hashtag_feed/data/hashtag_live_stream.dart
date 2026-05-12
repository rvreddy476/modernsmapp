// Minimal SSE client for the hashtag real-time stream. Connects to
// GET /v1/hashtags/<tag>/stream, parses incoming text into SSE
// events, and forwards each `new_post` event as a tick on the
// returned broadcast stream.
//
// Not a general-purpose SSE library — only the subset post-service
// emits: `event: <name>\ndata: <json>\n\n`, optionally preceded by
// `:` keepalive comments. That's enough for the hashtag-feed pill.

import 'dart:async';
import 'dart:convert';

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:dio/dio.dart';

/// One decoded `new_post` event off the stream. We don't bind to a
/// strict shape so the SSE payload can grow without forcing a client
/// rebuild — the UI only cares "something new happened".
class HashtagStreamEvent {
  const HashtagStreamEvent(this.payload);

  /// Decoded JSON body of the `data:` line. Empty map on parse failure
  /// (e.g. heartbeat — though those are filtered above).
  final Map<String, dynamic> payload;
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

    _open(url, cancelToken, controller);
    return controller.stream;
  }

  Future<void> _open(
    String url,
    CancelToken cancelToken,
    StreamController<HashtagStreamEvent> controller,
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
          final parsed = _parseEvent(raw);
          if (parsed != null) controller.add(parsed);
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

  /// Parses one SSE event block. Returns null when:
  /// - the block is a `:` keepalive comment, or
  /// - the event name isn't `new_post`, or
  /// - the data line isn't JSON.
  HashtagStreamEvent? _parseEvent(String raw) {
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
    if (event != 'new_post' || dataLines.isEmpty) return null;
    try {
      final payload = jsonDecode(dataLines.join('\n')) as Map<String, dynamic>;
      return HashtagStreamEvent(payload);
    } catch (_) {
      return null;
    }
  }
}
