import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// One assignment handed to a reviewer (creator identity is blinded server-side).
class ReviewAssignment {
  final String id;
  final String contentId;
  final int contentSeconds;

  const ReviewAssignment({
    required this.id,
    required this.contentId,
    this.contentSeconds = 0,
  });

  factory ReviewAssignment.fromJson(Map<String, dynamic> json) => ReviewAssignment(
        id: (json['id'] ?? '').toString(),
        contentId: (json['content_id'] ?? '').toString(),
        contentSeconds: (json['content_seconds'] as num?)?.toInt() ?? 0,
      );
}

/// Review feedback shown to a creator whose video needs changes.
class ReviewFeedback {
  final String status; // open | resolved
  final String? adminDecision; // reject | request_edits | approve
  final String? adminNotes;
  final String reviewerComments;

  const ReviewFeedback({
    required this.status,
    this.adminDecision,
    this.adminNotes,
    this.reviewerComments = '',
  });

  factory ReviewFeedback.fromJson(Map<String, dynamic> json) => ReviewFeedback(
        status: (json['status'] ?? '').toString(),
        adminDecision: json['admin_decision']?.toString(),
        adminNotes: json['admin_notes']?.toString(),
        reviewerComments: (json['reviewer_comments'] ?? '').toString(),
      );
}

/// Reviewer dashboard summary.
class ReviewerDashboard {
  final bool isReviewer;
  final String status;
  final String tier;
  final double accuracy;
  final bool kycVerified;
  final int reviewsCompleted;
  final int escalated;
  final int lifetimeEarnedPaise;
  final int pendingQueue;

  const ReviewerDashboard({
    required this.isReviewer,
    this.status = '',
    this.tier = '',
    this.accuracy = 0,
    this.kycVerified = false,
    this.reviewsCompleted = 0,
    this.escalated = 0,
    this.lifetimeEarnedPaise = 0,
    this.pendingQueue = 0,
  });

  factory ReviewerDashboard.fromJson(Map<String, dynamic> json) {
    final r = json['reviewer'];
    final rev = r is Map<String, dynamic> ? r : null;
    return ReviewerDashboard(
      isReviewer: rev != null,
      status: (rev?['status'] ?? '').toString(),
      tier: (rev?['tier'] ?? '').toString(),
      accuracy: (rev?['reviewer_accuracy'] as num?)?.toDouble() ?? 0,
      kycVerified: rev?['kyc_verified'] == true,
      reviewsCompleted: (json['reviews_completed'] as num?)?.toInt() ?? 0,
      escalated: (json['escalated'] as num?)?.toInt() ?? 0,
      lifetimeEarnedPaise: (json['lifetime_earned_paise'] as num?)?.toInt() ?? 0,
      pendingQueue: (json['pending_queue'] as num?)?.toInt() ?? 0,
    );
  }
}

/// Reviewer + creator-loop API (reviewer-service via the gateway).
class ReviewerRepository {
  final ApiClient _api;
  ReviewerRepository(this._api);

  Future<ReviewerDashboard> dashboard() async {
    final res = await _api.get('/v1/reviewer/me/stats');
    return ReviewerDashboard.fromJson(_obj(res.data) ?? const {});
  }

  Future<void> optIn({List<String> languages = const ['en'], String region = ''}) async {
    await _api.post('/v1/reviewer/opt-in',
        data: {'languages': languages, 'region': region});
  }

  /// Syncs identity-verification status from wallet-service. Returns verified.
  Future<bool> verifyKyc() async {
    final res = await _api.post('/v1/reviewer/verify-kyc');
    final d = _obj(res.data);
    return d?['kyc_verified'] == true;
  }

  /// Next assignment to review, or null when the queue is empty / at capacity.
  Future<ReviewAssignment?> next() async {
    final res = await _api.get('/v1/reviewer/assignments/next');
    final data = _obj(res.data);
    if (data == null || (data['id'] ?? '').toString().isEmpty) return null;
    return ReviewAssignment.fromJson(data);
  }

  Future<void> heartbeat(String assignmentId, int seconds) async {
    await _api.post('/v1/reviewer/assignments/$assignmentId/heartbeat',
        data: {'seconds': seconds});
  }

  /// [decision] is 'approve' or 'escalate'. Comments are required when escalating.
  Future<void> decide(String assignmentId, String decision, {String comments = ''}) async {
    await _api.post('/v1/reviewer/assignments/$assignmentId/decision',
        data: {'decision': decision, 'comments': comments});
  }

  /// Latest review feedback for the caller's own content (null if none).
  Future<ReviewFeedback?> feedback(String contentId) async {
    try {
      final res = await _api.get('/v1/reviewer/content/$contentId/feedback');
      final data = _obj(res.data);
      return data == null ? null : ReviewFeedback.fromJson(data);
    } catch (_) {
      return null;
    }
  }

  /// Creator re-submits an edited post (needs_changes → back into review).
  Future<void> resubmit(String postId) async {
    await _api.post('/v1/posts/$postId/resubmit');
  }
}

Map<String, dynamic>? _obj(dynamic body) {
  if (body is Map<String, dynamic>) {
    final data = body['data'];
    if (data is Map<String, dynamic>) return data;
    if (data == null) return null;
  }
  return null;
}

final reviewerRepositoryProvider = Provider<ReviewerRepository>((ref) {
  return ReviewerRepository(ref.watch(apiClientProvider));
});
