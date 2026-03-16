import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class StudioRepository {
  final ApiClient _api;
  StudioRepository(this._api);

  Future<List<Map<String, dynamic>>> getStickerPacks() async {
    final res = await _api.get('/v1/studio/sticker-packs');
    final data = res.data['data'] ?? res.data;
    return List<Map<String, dynamic>>.from(data['packs'] ?? data ?? []);
  }

  Future<List<Map<String, dynamic>>> getStickers({String? category, int limit = 20}) async {
    final res = await _api.get('/v1/studio/stickers', queryParameters: {
      'category': category,
      'limit': limit,
    });
    final data = res.data['data'] ?? res.data;
    return List<Map<String, dynamic>>.from(data['stickers'] ?? data ?? []);
  }

  Future<void> recordStickerUse(String stickerId) async {
    await _api.post('/v1/studio/stickers/$stickerId/use');
  }

  Future<List<Map<String, dynamic>>> getTemplates({String? category}) async {
    final res = await _api.get('/v1/studio/templates', queryParameters: {
      'category': category,
      'limit': 20,
    });
    final data = res.data['data'] ?? res.data;
    return List<Map<String, dynamic>>.from(data['templates'] ?? data ?? []);
  }

  Future<List<Map<String, dynamic>>> getEditorSessions() async {
    final res = await _api.get('/v1/studio/sessions');
    final data = res.data['data'] ?? res.data;
    return List<Map<String, dynamic>>.from(data['sessions'] ?? data ?? []);
  }

  Future<List<Map<String, dynamic>>> getSuggestedFrames(String mediaAssetId) async {
    final res = await _api.get('/v1/media/$mediaAssetId/suggested-frames');
    final data = res.data['data'] ?? res.data;
    return List<Map<String, dynamic>>.from(data['frames'] ?? []);
  }

  Future<void> setCoverFrame(String mediaAssetId, int offsetMs) async {
    await _api.post('/v1/media/$mediaAssetId/cover-frame', data: {'offset_ms': offsetMs});
  }
}

final studioRepositoryProvider = Provider<StudioRepository>((ref) {
  return StudioRepository(ref.watch(apiClientProvider));
});
