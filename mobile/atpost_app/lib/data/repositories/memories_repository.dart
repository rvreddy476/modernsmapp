import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/memory.dart';
import 'package:atpost_app/data/models/slambook.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class MemoriesRepository {
  final ApiClient _api;

  MemoriesRepository(this._api);

  /// Get "On This Day" memories.
  Future<List<OnThisDayMemory>> getOnThisDay() async {
    final response = await _api.get('${Environment.memoriesPath}/on-this-day');
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items.map((e) => OnThisDayMemory.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// List memory collections.
  Future<List<MemoryCollection>> getCollections({int limit = 20}) async {
    final response = await _api.get(
      '${Environment.memoriesPath}/collections',
      queryParameters: {'limit': limit},
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items.map((e) => MemoryCollection.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Create a memory collection.
  Future<MemoryCollection> createCollection({
    required String title,
    String description = '',
    String visibility = 'private',
  }) async {
    final response = await _api.post(
      '${Environment.memoriesPath}/collections',
      data: {'title': title, 'description': description, 'visibility': visibility},
    );
    return MemoryCollection.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Add item to a collection.
  Future<void> addCollectionItem(String collectionId, {String? postId, String? mediaUrl, String caption = ''}) async {
    await _api.post(
      '${Environment.memoriesPath}/collections/$collectionId/items',
      data: {'post_id': postId, 'media_url': mediaUrl, 'caption': caption},
    );
  }

  /// Delete a collection.
  Future<void> deleteCollection(String collectionId) async {
    await _api.delete('${Environment.memoriesPath}/collections/$collectionId');
  }

  /// List active SlamBook template packs.
  Future<List<SlambookTemplatePack>> getSlambookTemplatePacks() async {
    final response = await _api.get('${Environment.memoriesPath}/slambook-template-packs');
    final items = _dataItems(response);
    return items.map((item) => SlambookTemplatePack.fromJson(item as Map<String, dynamic>)).toList();
  }

  /// Create a SlamBook from a template pack and/or custom cards.
  Future<Slambook> createSlambook({
    required String title,
    String subtitle = '',
    String description = '',
    String category = 'personal',
    String themeKey = 'classic',
    String visibility = 'invited_only',
    String responseIdentityMode = 'named',
    bool approvalRequired = false,
    String templatePackKey = '',
    List<SlambookCardDraft> customCards = const [],
    DateTime? closesAt,
  }) async {
    final response = await _api.post(
      '${Environment.memoriesPath}/slambooks',
      data: {
        'title': title,
        'subtitle': subtitle,
        'description': description,
        'category': category,
        'theme_key': themeKey,
        'visibility': visibility,
        'response_identity_mode': responseIdentityMode,
        'approval_required': approvalRequired,
        'template_pack_key': templatePackKey,
        if (closesAt != null) 'closes_at': closesAt.toUtc().toIso8601String(),
        'custom_cards': customCards.map((card) => card.toJson()).toList(),
      },
    );
    return Slambook.fromJson(_dataMap(response));
  }

  /// List a user's SlamBooks.
  Future<List<Slambook>> getSlambooks({required String ownerUserId}) async {
    final response = await _api.get(
      '${Environment.memoriesPath}/slambooks',
      queryParameters: {'owner_user_id': ownerUserId},
    );
    final items = _dataItems(response);
    return items.map((item) => Slambook.fromJson(item as Map<String, dynamic>)).toList();
  }

  /// Fetch a full SlamBook detail payload.
  Future<SlambookDetail> getSlambook(String slambookId) async {
    final response = await _api.get('${Environment.memoriesPath}/slambooks/$slambookId');
    return SlambookDetail.fromJson(_dataMap(response));
  }

  /// Create or fetch a share link for a SlamBook.
  Future<SlambookInvite> createSlambookShareLink(String slambookId) async {
    final response = await _api.post('${Environment.memoriesPath}/slambooks/$slambookId/share-link');
    return SlambookInvite.fromJson(_dataMap(response));
  }

  /// Invite specific users to answer a SlamBook.
  Future<List<SlambookInvite>> createSlambookInvites(
    String slambookId, {
    required List<String> targetUserIds,
    String? message,
  }) async {
    final response = await _api.post(
      '${Environment.memoriesPath}/slambooks/$slambookId/invites',
      data: {
        'target_user_ids': targetUserIds,
        ...?(message == null ? null : {'message': message}),
      },
    );
    final items = _dataItems(response);
    return items.map((item) => SlambookInvite.fromJson(item as Map<String, dynamic>)).toList();
  }

  /// Save or submit a response session for a SlamBook.
  Future<SlambookResponseSession> saveSlambookResponse(
    String slambookId, {
    String displayName = '',
    bool anonymous = false,
    String? shareToken,
    bool submit = false,
    List<SlambookResponseAnswerDraft> answers = const [],
  }) async {
    final response = await _api.post(
      '${Environment.memoriesPath}/slambooks/$slambookId/responses',
      data: {
        'display_name': displayName,
        'anonymous': anonymous,
        ...?(shareToken == null ? null : {'share_token': shareToken}),
        'submit': submit,
        'answers': answers.map((answer) => answer.toJson()).toList(),
      },
    );
    return SlambookResponseSession.fromJson(_dataMap(response));
  }

  /// Fetch the opinion board for a SlamBook.
  Future<List<SlambookOpinionSpaceItem>> getSlambookOpinionSpace(String slambookId) async {
    final response = await _api.get('${Environment.memoriesPath}/slambooks/$slambookId/opinion-space');
    final items = _dataItems(response);
    return items.map((item) => SlambookOpinionSpaceItem.fromJson(item as Map<String, dynamic>)).toList();
  }

  /// Fetch the pending moderation queue.
  Future<List<SlambookResponseSession>> getSlambookModerationQueue(String slambookId) async {
    final response = await _api.get('${Environment.memoriesPath}/slambooks/$slambookId/moderation');
    final items = _dataItems(response);
    return items.map((item) => SlambookResponseSession.fromJson(item as Map<String, dynamic>)).toList();
  }

  /// Moderate a submitted SlamBook session.
  Future<void> moderateSlambookSession(
    String slambookId,
    String sessionId, {
    required String action,
    String reason = '',
  }) async {
    await _api.post(
      '${Environment.memoriesPath}/slambooks/$slambookId/moderation/$sessionId',
      data: {'action': action, 'reason': reason},
    );
  }

  /// Pin or unpin an opinion-board item.
  Future<void> pinSlambookOpinionItem(
    String slambookId,
    String itemId, {
    bool pinned = true,
  }) async {
    await _api.post(
      '${Environment.memoriesPath}/slambooks/$slambookId/opinion-space/$itemId/pin',
      data: {'pinned': pinned},
    );
  }

  /// Reorder opinion-board items.
  Future<void> reorderSlambookOpinionItems(
    String slambookId,
    List<String> itemIds,
  ) async {
    await _api.post(
      '${Environment.memoriesPath}/slambooks/$slambookId/opinion-space/reorder',
      data: {'item_ids': itemIds},
    );
  }

  /// Archive a SlamBook.
  Future<void> archiveSlambook(String slambookId) async {
    await _api.post('${Environment.memoriesPath}/slambooks/$slambookId/archive');
  }

  /// Resolve a SlamBook from a share token.
  Future<SlambookDetail> getSlambookByShareToken(String shareToken) async {
    final response = await _api.get('${Environment.memoriesPath}/share/$shareToken');
    return SlambookDetail.fromJson(_dataMap(response));
  }
}

final memoriesRepositoryProvider = Provider<MemoriesRepository>((ref) {
  return MemoriesRepository(ref.watch(apiClientProvider));
});

Map<String, dynamic> _dataMap(dynamic response) {
  final payload = response.data;
  if (payload is Map<String, dynamic>) {
    final data = payload['data'];
    if (data is Map<String, dynamic>) {
      return data;
    }
    if (data is Map) {
      return data.map((key, dynamic value) => MapEntry(key.toString(), value));
    }
    return payload;
  }
  return const {};
}

List<dynamic> _dataItems(dynamic response) {
  final payload = response.data;
  if (payload is Map<String, dynamic>) {
    final data = payload['data'];
    if (data is Map<String, dynamic>) {
      final items = data['items'];
      if (items is List<dynamic>) {
        return items;
      }
    }
    if (data is List<dynamic>) {
      return data;
    }
  }
  return const <dynamic>[];
}
