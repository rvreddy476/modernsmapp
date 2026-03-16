import 'package:atpost_app/data/models/profile_extras.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ProfileExtrasRepository {
  final ApiClient _api;

  ProfileExtrasRepository(this._api);

  Future<List<ProfilePin>> getMyPins() async {
    final response = await _api.get('/v1/users/me/pins');
    final items =
        (response.data['data']?['items'] as List<dynamic>?) ??
        (response.data['data'] as List<dynamic>?) ??
        [];
    return items
        .map((e) => ProfilePin.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<List<ProfilePin>> getUserPins(String userId) async {
    final response = await _api.get('/v1/users/$userId/pins');
    final items =
        (response.data['data']?['items'] as List<dynamic>?) ??
        (response.data['data'] as List<dynamic>?) ??
        [];
    return items
        .map((e) => ProfilePin.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<void> pinContent(String contentType, String contentId) async {
    await _api.post('/v1/users/me/pins', data: {
      'content_type': contentType,
      'content_id': contentId,
    });
  }

  Future<void> unpinContent(String pinId) async {
    await _api.delete('/v1/users/me/pins/$pinId');
  }

  Future<List<PortfolioItem>> getMyPortfolio() async {
    final response = await _api.get('/v1/users/me/portfolio');
    final items =
        (response.data['data']?['items'] as List<dynamic>?) ??
        (response.data['data'] as List<dynamic>?) ??
        [];
    return items
        .map((e) => PortfolioItem.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<List<PortfolioItem>> getUserPortfolio(String userId) async {
    final response = await _api.get('/v1/users/$userId/portfolio');
    final items =
        (response.data['data']?['items'] as List<dynamic>?) ??
        (response.data['data'] as List<dynamic>?) ??
        [];
    return items
        .map((e) => PortfolioItem.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<void> addPortfolioItem({
    required String title,
    String? description,
    String? url,
    String itemType = 'project',
  }) async {
    await _api.post('/v1/users/me/portfolio', data: {
      'title': title,
      'description': description,
      'url': url,
      'item_type': itemType,
    });
  }

  Future<void> updatePortfolioItem(
    String id,
    Map<String, dynamic> fields,
  ) async {
    await _api.patch('/v1/users/me/portfolio/$id', data: fields);
  }

  Future<void> deletePortfolioItem(String id) async {
    await _api.delete('/v1/users/me/portfolio/$id');
  }

  Future<ProfileQrCode> getMyQrCode() async {
    final response = await _api.get('/v1/users/me/qr');
    final data = response.data['data'] ?? response.data;
    return ProfileQrCode.fromJson(data as Map<String, dynamic>);
  }
}

final profileExtrasRepositoryProvider =
    Provider<ProfileExtrasRepository>((ref) {
  return ProfileExtrasRepository(ref.watch(apiClientProvider));
});
