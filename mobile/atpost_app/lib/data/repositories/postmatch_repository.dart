import 'dart:io';

import 'package:atpost_app/data/models/postmatch.dart';
import 'package:atpost_app/services/postmatch_api_client.dart';
import 'package:atpost_app/services/postmatch_auth_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class PostMatchRepository {
  final PostMatchApiClient _api;
  final PostMatchAuthService _auth;

  PostMatchRepository(this._api, this._auth);

  Future<PostMatchProfile?> getProfile() async {
    try {
      final response = await _api.get('/api/v1/me/profile');
      final data = _unwrapData(response.data);
      if (data is Map<String, dynamic>) {
        return PostMatchProfile.fromJson(data);
      }
      return null;
    } on DioException catch (error) {
      if (error.response?.statusCode == 404) return null;
      rethrow;
    }
  }

  Future<PostMatchProfile> updateProfile(Map<String, dynamic> payload) async {
    final response = await _api.put('/api/v1/me/profile', data: payload);
    return PostMatchProfile.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  Future<PostMatchPreferences?> getPreferences() async {
    try {
      final response = await _api.get('/api/v1/me/preferences');
      final data = _unwrapData(response.data);
      if (data is Map<String, dynamic>) {
        return PostMatchPreferences.fromJson(data);
      }
      return null;
    } on DioException catch (error) {
      if (error.response?.statusCode == 404) return null;
      rethrow;
    }
  }

  Future<PostMatchPreferences> updatePreferences(
    Map<String, dynamic> payload,
  ) async {
    final response = await _api.put('/api/v1/me/preferences', data: payload);
    return PostMatchPreferences.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  Future<List<PostMatchPhoto>> getPhotos() async {
    final response = await _api.get('/api/v1/me/profile/photos');
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map((item) => PostMatchPhoto.fromJson(Map<String, dynamic>.from(item)))
        .toList();
  }

  Future<PostMatchInitUpload> initPhotoUpload({
    required String contentType,
    required String fileName,
    required int fileSize,
  }) async {
    final response = await _api.post(
      '/api/v1/media/init',
      data: {
        'purpose': 'profile_photo',
        'content_type': contentType,
        'file_name': fileName,
        'file_size': fileSize,
      },
    );
    return PostMatchInitUpload.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  Future<void> uploadFileToPresignedUrl({
    required String uploadUrl,
    required File file,
    required String contentType,
    ProgressCallback? onSendProgress,
  }) {
    return _api.uploadToPresignedUrl(
      uploadUrl: uploadUrl,
      file: file,
      contentType: contentType,
      onSendProgress: onSendProgress,
    );
  }

  Future<PostMatchPhoto> completePhotoUpload({
    required String mediaId,
    required String mediaKey,
    required bool isPrimary,
  }) async {
    final response = await _api.post(
      '/api/v1/me/profile/photos/complete',
      data: {
        'media_id': mediaId,
        'media_key': mediaKey,
        'is_primary': isPrimary,
      },
    );
    return PostMatchPhoto.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  Future<void> deletePhoto(String photoId) async {
    await _api.delete('/api/v1/me/profile/photos/$photoId');
  }

  Future<List<PostMatchFeedItem>> getDiscoveryFeed({String? cursor}) async {
    final response = await _api.get(
      '/api/v1/discovery/feed',
      queryParameters: {
        'limit': 20,
        if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
      },
    );
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map(
          (item) => PostMatchFeedItem.fromJson(Map<String, dynamic>.from(item)),
        )
        .toList();
  }

  Future<PostMatchDecisionResult> makeDecision({
    required String targetUserId,
    required String decision,
  }) async {
    final response = await _api.post(
      '/api/v1/discovery/decision',
      data: {'target_user_id': targetUserId, 'decision': decision},
    );
    return PostMatchDecisionResult.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  Future<List<PostMatchMatch>> getMatches() async {
    final response = await _api.get('/api/v1/matches');
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map((item) => PostMatchMatch.fromJson(Map<String, dynamic>.from(item)))
        .toList();
  }

  Future<List<PostMatchLikeReceived>> getLikesReceived() async {
    final response = await _api.get('/api/v1/matches/likes-received');
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map(
          (item) =>
              PostMatchLikeReceived.fromJson(Map<String, dynamic>.from(item)),
        )
        .toList();
  }

  Future<void> unmatch(String matchId) async {
    await _api.delete('/api/v1/matches/$matchId');
  }

  Future<List<PostMatchConversation>> getConversations() async {
    final response = await _api.get('/api/v1/conversations');
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map(
          (item) =>
              PostMatchConversation.fromJson(Map<String, dynamic>.from(item)),
        )
        .toList();
  }

  Future<List<PostMatchMessage>> getMessages(
    String conversationId, {
    String? cursor,
  }) async {
    final response = await _api.get(
      '/api/v1/conversations/$conversationId/messages',
      queryParameters: {
        'limit': 50,
        if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
      },
    );
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map(
          (item) => PostMatchMessage.fromJson(Map<String, dynamic>.from(item)),
        )
        .toList();
  }

  Future<PostMatchMessage> sendMessage(
    String conversationId, {
    required String bodyText,
  }) async {
    final response = await _api.post(
      '/api/v1/conversations/$conversationId/messages',
      data: {'message_type': 'text', 'body_text': bodyText},
    );
    return PostMatchMessage.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  Future<void> logout() async {
    try {
      await _api.post('/api/v1/auth/logout');
    } finally {
      await _auth.clearSession();
    }
  }

  Future<void> setOnboardingStatus(String status) {
    return _auth.updateOnboardingStatus(status);
  }

  dynamic _unwrapData(dynamic body) {
    if (body is Map<String, dynamic> && body.containsKey('data')) {
      return body['data'];
    }
    return body;
  }
}

final postMatchRepositoryProvider = Provider<PostMatchRepository>((ref) {
  return PostMatchRepository(
    ref.watch(postMatchApiClientProvider),
    ref.watch(postMatchAuthServiceProvider),
  );
});
