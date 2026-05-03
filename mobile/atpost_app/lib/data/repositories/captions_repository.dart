import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Subtitle / closed-caption track exposed by media-service. The
/// recon doc (§B.3) confirms the route is `GET /v1/subtitles/:mediaId`
/// and entries persist with a language code (e.g. `en`, `hi`), a
/// source (`auto` or `manual`), and a content URL pointing to a VTT
/// or SRT blob.
class CaptionTrack {
  const CaptionTrack({
    required this.id,
    required this.mediaId,
    required this.language,
    required this.source,
    required this.contentUrl,
    this.format,
    this.confidence,
  });

  final String id;
  final String mediaId;
  final String language;
  final String source;
  final String contentUrl;
  final String? format;
  final double? confidence;

  factory CaptionTrack.fromJson(Map<String, dynamic> json) {
    return CaptionTrack(
      id: (json['id'] ?? '').toString(),
      mediaId: (json['media_id'] ?? '').toString(),
      language: (json['language'] ?? '').toString(),
      source: (json['source'] ?? '').toString(),
      contentUrl: (json['content_url'] ?? json['url'] ?? '').toString(),
      format: json['format']?.toString(),
      confidence: _toDouble(json['confidence']),
    );
  }
}

double? _toDouble(dynamic v) {
  if (v == null) return null;
  if (v is double) return v;
  if (v is int) return v.toDouble();
  if (v is String) return double.tryParse(v);
  return null;
}

/// Thin client for the captions surface on media-service.
///
/// The watch screen (PostTube) and reels player both call
/// [listForMedia] when the player opens; if the result is empty the
/// CC button is hidden, otherwise the toggle picks the user-preferred
/// language (defaulting to English) and renders the VTT cue overlay.
class CaptionsRepository {
  CaptionsRepository(this._api);

  final ApiClient _api;

  Future<List<CaptionTrack>> listForMedia(String mediaId) async {
    if (mediaId.isEmpty) return const <CaptionTrack>[];
    try {
      final response = await _api.get('/v1/subtitles/$mediaId');
      final body = response.data;
      // The handler returns `{ "subtitles": [...] }` directly when
      // `data` envelope is unwrapped, otherwise we have to dig.
      List<dynamic>? items;
      if (body is Map<String, dynamic>) {
        final data = body['data'];
        if (data is Map<String, dynamic>) {
          final raw = data['subtitles'];
          if (raw is List) items = raw;
        }
        if (items == null) {
          final raw = body['subtitles'];
          if (raw is List) items = raw;
        }
      }
      if (items == null) return const <CaptionTrack>[];
      return items
          .whereType<Map>()
          .map((e) => CaptionTrack.fromJson(Map<String, dynamic>.from(e)))
          .toList();
    } catch (_) {
      // Captions are best-effort; the player must keep working when
      // the captions endpoint is unavailable.
      return const <CaptionTrack>[];
    }
  }
}

final captionsRepositoryProvider = Provider<CaptionsRepository>((ref) {
  return CaptionsRepository(ref.watch(apiClientProvider));
});

/// FutureProvider.autoDispose.family that fetches caption tracks for
/// a given media id. Riverpod cancels the request if the player
/// closes before the response lands.
final captionsForMediaProvider = FutureProvider.autoDispose
    .family<List<CaptionTrack>, String>((ref, mediaId) async {
  final repo = ref.watch(captionsRepositoryProvider);
  return repo.listForMedia(mediaId);
});
