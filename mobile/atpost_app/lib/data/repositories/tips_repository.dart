import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// One fan→creator transfer. Mirrors the JSON the backend returns
/// from /v1/monetization/tips* endpoints.
class Tip {
  final String id;
  final String senderId;
  final String recipientId;
  final int amountPaise;
  final String currency;
  final String? message;
  final String? postId;
  final String? streamId;
  final String status;
  final DateTime createdAt;

  const Tip({
    required this.id,
    required this.senderId,
    required this.recipientId,
    required this.amountPaise,
    required this.currency,
    this.message,
    this.postId,
    this.streamId,
    required this.status,
    required this.createdAt,
  });

  factory Tip.fromJson(Map<String, dynamic> json) {
    return Tip(
      id: (json['id'] ?? '').toString(),
      senderId: (json['sender_id'] ?? '').toString(),
      recipientId: (json['recipient_id'] ?? '').toString(),
      amountPaise: (json['amount_paise'] is num)
          ? (json['amount_paise'] as num).toInt()
          : 0,
      currency: (json['currency'] ?? 'INR').toString(),
      message: json['message']?.toString(),
      postId: json['post_id']?.toString(),
      streamId: json['stream_id']?.toString(),
      status: (json['status'] ?? 'completed').toString(),
      createdAt:
          DateTime.tryParse(json['created_at']?.toString() ?? '') ??
          DateTime.now(),
    );
  }
}

/// Thrown by [TipsRepository.send] when the backend rejects with a
/// known semantic error code, so the UI can render the right copy
/// instead of generic "something went wrong".
class TipError implements Exception {
  final String code; // e.g. DAILY_TIP_CAP_EXCEEDED, CHARGE_FAILED
  final String message;
  TipError(this.code, this.message);
  @override
  String toString() => '$code: $message';
}

class TipsRepository {
  final ApiClient _api;
  TipsRepository(this._api);

  /// Send a one-shot tip. Throws [TipError] on rejection.
  Future<Tip> send({
    required String recipientId,
    required int amountPaise,
    String? message,
    String? postId,
    String? streamId,
  }) async {
    try {
      final res = await _api.post(
        '/v1/monetization/tips',
        data: {
          'recipient_id': recipientId,
          'amount_paise': amountPaise,
          if (message != null && message.isNotEmpty) 'message': message,
          'post_id': ?postId,
          'stream_id': ?streamId,
        },
      );
      final data = res.data['data'] as Map<String, dynamic>;
      final tipJson = data['tip'] as Map<String, dynamic>;
      return Tip.fromJson(tipJson);
    } catch (e) {
      // Try to read the structured backend error envelope.
      final dynamic resp = (e as dynamic).response;
      final dynamic errObj = resp?.data?['error'];
      if (errObj is Map<String, dynamic>) {
        final code = errObj['code']?.toString() ?? 'TIP_FAILED';
        final msg = errObj['message']?.toString() ?? 'Tip failed';
        throw TipError(code, msg);
      }
      rethrow;
    }
  }

  Future<List<Tip>> listSent({String? cursor, int limit = 50}) async {
    final res = await _api.get(
      '/v1/monetization/tips/sent',
      queryParameters: {
        if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
        'limit': limit,
      },
    );
    final list = (res.data['data'] as List<dynamic>?) ?? [];
    return list
        .map((e) => Tip.fromJson(Map<String, dynamic>.from(e as Map)))
        .toList();
  }

  Future<List<Tip>> listReceived({String? cursor, int limit = 50}) async {
    final res = await _api.get(
      '/v1/monetization/tips/received',
      queryParameters: {
        if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
        'limit': limit,
      },
    );
    final list = (res.data['data'] as List<dynamic>?) ?? [];
    return list
        .map((e) => Tip.fromJson(Map<String, dynamic>.from(e as Map)))
        .toList();
  }
}

final tipsRepositoryProvider = Provider<TipsRepository>((ref) {
  return TipsRepository(ref.watch(apiClientProvider));
});
