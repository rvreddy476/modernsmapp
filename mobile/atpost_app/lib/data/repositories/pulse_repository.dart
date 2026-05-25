import 'dart:io';

import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/services/pulse_api_client.dart';
import 'package:atpost_app/services/pulse_auth_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

// formerly PostMatchRepository
class PulseRepository {
  final PulseApiClient _api;
  final PulseAuthService _auth;

  PulseRepository(this._api, this._auth);

  Future<PulseProfile?> getProfile() async {
    try {
      final response = await _api.get('/v1/dating/profile');
      final data = _unwrapData(response.data);
      if (data is Map<String, dynamic>) {
        return PulseProfile.fromJson(data);
      }
      return null;
    } on DioException catch (error) {
      if (error.response?.statusCode == 404) return null;
      rethrow;
    }
  }

  Future<PulseProfile> updateProfile(Map<String, dynamic> payload) async {
    // P0-1: backend is POST, not PUT — UpsertProfile semantics.
    final response = await _api.post('/v1/dating/profile', data: payload);
    return PulseProfile.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  Future<PulsePreferences?> getPreferences() async {
    try {
      final response = await _api.get('/v1/dating/preferences');
      final data = _unwrapData(response.data);
      if (data is Map<String, dynamic>) {
        return PulsePreferences.fromJson(data);
      }
      return null;
    } on DioException catch (error) {
      if (error.response?.statusCode == 404) return null;
      rethrow;
    }
  }

  Future<PulsePreferences> updatePreferences(
    Map<String, dynamic> payload,
  ) async {
    final response = await _api.put('/v1/dating/preferences', data: payload);
    return PulsePreferences.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  Future<List<PulsePhoto>> getPhotos() async {
    // P0-1: backend route is /v1/dating/photos (no /profile prefix).
    final response = await _api.get('/v1/dating/photos');
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map((item) => PulsePhoto.fromJson(Map<String, dynamic>.from(item)))
        .toList();
  }

  Future<PulseInitUpload> initPhotoUpload({
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
    return PulseInitUpload.fromJson(
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

  Future<PulsePhoto> completePhotoUpload({
    required String mediaId,
    required String mediaKey,
    required bool isPrimary,
  }) async {
    // P0-1: backend route is POST /v1/dating/photos (no /profile prefix,
    // no /complete suffix). Body mirrors dating-service CreatePhotoParams.
    final response = await _api.post(
      '/v1/dating/photos',
      data: {
        'media_id': mediaId,
        'media_key': mediaKey,
        'is_primary': isPrimary,
      },
    );
    return PulsePhoto.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  Future<void> deletePhoto(String photoId) async {
    // P0-1: backend route is /v1/dating/photos/:id.
    await _api.delete('/v1/dating/photos/$photoId');
  }

  Future<List<PulseFeedItem>> getDiscoveryFeed({String? cursor}) async {
    final response = await _api.get(
      '/v1/dating/pulse/today',
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
          (item) => PulseFeedItem.fromJson(Map<String, dynamic>.from(item)),
        )
        .toList();
  }

  /// Sprint 2: New Pulse "today" feed.
  ///
  /// Backend: `GET /v1/dating/pulse/today` — returns an envelope of
  /// `{ data: [PulseCard...], meta: { generated_at, size } }`.
  Future<PulsePage> getPulseToday() async {
    final response = await _api.get('/v1/dating/pulse/today');
    final body = response.data;
    if (body is Map<String, dynamic>) {
      return PulsePage.fromJson(body);
    }
    return const PulsePage.empty();
  }

  /// Sprint 2: Nebula — secondary candidate pool (passed / queued / etc.).
  ///
  /// `filter` mirrors the query parameter (default "passed").
  Future<PulsePage> getPulseNebula({
    String filter = 'passed',
    int limit = 50,
  }) async {
    final response = await _api.get(
      '/v1/dating/pulse/nebula',
      queryParameters: {'filter': filter, 'limit': limit},
    );
    final body = response.data;
    if (body is Map<String, dynamic>) {
      return PulsePage.fromJson(body);
    }
    return const PulsePage.empty();
  }

  /// Routes the legacy "decision" model to the new spark/stash/pass
  /// triad. The dating-service no longer exposes /v1/dating/decision —
  /// each verb hits a dedicated route. Returns a normalised
  /// PulseDecisionResult so the existing UI binding doesn't need to
  /// change. P0-1 contract realignment.
  Future<PulseDecisionResult> makeDecision({
    required String targetUserId,
    required String decision,
  }) async {
    switch (decision) {
      case 'spark':
      case 'like': // legacy alias
        final response = await _api.post(
          '/v1/dating/sparks',
          data: {
            'to_user_id': targetUserId,
            'target_kind': 'profile',
            'target_ref': targetUserId,
          },
        );
        final data = Map<String, dynamic>.from(
          _unwrapData(response.data) as Map,
        );
        // CreateSpark returns {spark, match_id?, matched?}. Adapt to
        // PulseDecisionResult's shape.
        return PulseDecisionResult.fromJson({
          'decision': 'spark',
          'target_user_id': targetUserId,
          'match_formed': data['matched'] == true,
          if (data['match_id'] != null) 'match_id': data['match_id'],
        });
      case 'stash':
      case 'save': // legacy alias
        await _api.post(
          '/v1/dating/stash',
          data: {'candidate_id': targetUserId},
        );
        return PulseDecisionResult.fromJson({
          'decision': 'stash',
          'target_user_id': targetUserId,
          'match_formed': false,
        });
      case 'pass':
      default:
        // No dedicated /pass endpoint today; record a stash-remove if
        // present and otherwise emit a local-only decision. The pulse
        // deck on the server already filters via dating_passes; a
        // future revision should expose POST /v1/dating/passes.
        return PulseDecisionResult.fromJson({
          'decision': 'pass',
          'target_user_id': targetUserId,
          'match_formed': false,
        });
    }
  }

  Future<List<PulseMatch>> getMatches() async {
    final response = await _api.get('/v1/dating/matches');
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map((item) => PulseMatch.fromJson(Map<String, dynamic>.from(item)))
        .toList();
  }

  Future<List<PulseLikeReceived>> getLikesReceived() async {
    // P0-1: "likes received" maps to incoming sparks on the new model.
    final response = await _api.get('/v1/dating/sparks/incoming');
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map(
          (item) =>
              PulseLikeReceived.fromJson(Map<String, dynamic>.from(item)),
        )
        .toList();
  }

  Future<void> unmatch(String matchId) async {
    // P0-1: backend route is POST /v1/dating/matches/:id/close, not DELETE.
    await _api.post('/v1/dating/matches/$matchId/close');
  }

  // P0-1 + P0-3: rebased onto chat-service (the canonical message-service
  // per PRODUCTION_GAP_ANALYSIS.md). Routes go through the api-gateway
  // /v1/chat/* prefix which proxies to chat-service:8092. The legacy
  // /api/v1/conversations/* surface is retired.
  //
  // Dating-context conversations carry source_app=dating and the
  // backend's send path applies dating_match-specific authz (P0-3).
  Future<List<PulseConversation>> getConversations() async {
    final response = await _api.get(
      '/v1/chat/conversations',
      queryParameters: {'source_app': 'dating'},
    );
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map(
          (item) =>
              PulseConversation.fromJson(Map<String, dynamic>.from(item)),
        )
        .toList();
  }

  Future<List<PulseMessage>> getMessages(
    String conversationId, {
    String? cursor,
  }) async {
    final response = await _api.get(
      '/v1/chat/conversations/$conversationId/messages',
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
          (item) => PulseMessage.fromJson(Map<String, dynamic>.from(item)),
        )
        .toList();
  }

  Future<PulseMessage> sendMessage(
    String conversationId, {
    required String bodyText,
    String? idempotencyKey,
  }) async {
    final response = await _api.post(
      '/v1/chat/conversations/$conversationId/messages',
      data: {
        'message_type': 'text',
        'body_text': bodyText,
        if (idempotencyKey != null) 'idempotency_key': idempotencyKey,
      },
    );
    return PulseMessage.fromJson(
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

  /// Sprint 1: Pulse onboarding intent.
  Future<void> updateIntent(String intent) async {
    // P0-1: backend route is PATCH /v1/dating/profile/intent.
    await _api.patch<dynamic>(
      '/v1/dating/profile/intent',
      data: {'intent': intent},
    );
  }

  /// Sprint 1: Pulse onboarding — Tune axes (PUT /v1/dating/tune).
  Future<void> updateTune(Map<String, dynamic> payload) async {
    await _api.put<dynamic>('/v1/dating/tune', data: payload);
  }

  /// Sprint 1: Pulse onboarding — Echoes consent flag.
  Future<void> updateEchoesConsent(bool consent) async {
    await _api.patch<dynamic>(
      '/v1/dating/profile',
      data: {'echoes_consent': consent},
    );
  }

  Future<void> setOnboardingStatus(String status) {
    return _auth.updateOnboardingStatus(status);
  }

  // ---------------------------------------------------------------------
  // Sprint 3 — Sparks, Stash, Matches.
  //
  // These wrap the new dating-service endpoints introduced in S3:
  //   POST   /v1/dating/sparks
  //   GET    /v1/dating/sparks/incoming
  //   DELETE /v1/dating/sparks/:id
  //   GET    /v1/dating/stash
  //   POST   /v1/dating/stash
  //   DELETE /v1/dating/stash/:candidate_id
  //   GET    /v1/dating/matches
  //   GET    /v1/dating/matches/:id
  //   POST   /v1/dating/matches/:id/close
  //   POST   /v1/dating/matches/:id/extend
  // ---------------------------------------------------------------------

  /// Send a Spark. Returns the new spark id and (if mutual) the freshly
  /// created Match. The picker UI uses `match_formed` to switch into the
  /// celebration sheet.
  Future<SparkResult> createSpark({
    required String toUserId,
    required String targetKind,
    required String targetRef,
    String? note,
  }) async {
    final response = await _api.post(
      '/v1/dating/sparks',
      data: {
        'to_user_id': toUserId,
        'target_kind': targetKind,
        'target_ref': targetRef,
        if (note != null && note.isNotEmpty) 'note': note,
      },
    );
    return SparkResult.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  /// Revoke a previously-sent Spark.
  Future<void> revokeSpark(String sparkId) async {
    await _api.delete('/v1/dating/sparks/$sparkId');
  }

  /// Sparks somebody else sent at the viewer (paginated).
  Future<List<IncomingSpark>> getIncomingSparks({
    int limit = 30,
    int offset = 0,
  }) async {
    final response = await _api.get(
      '/v1/dating/sparks/incoming',
      queryParameters: {'limit': limit, 'offset': offset},
    );
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map(
          (item) => IncomingSpark.fromJson(Map<String, dynamic>.from(item)),
        )
        .toList();
  }

  /// Stash — slow-burn shelf of candidates the viewer wants to revisit.
  Future<List<PulseCard>> getStash() async {
    final response = await _api.get('/v1/dating/stash');
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map((item) => PulseCard.fromJson(Map<String, dynamic>.from(item)))
        .toList();
  }

  Future<void> addStash(String candidateId) async {
    await _api.post(
      '/v1/dating/stash',
      data: {'candidate_id': candidateId},
    );
  }

  Future<void> removeStash(String candidateId) async {
    await _api.delete('/v1/dating/stash/$candidateId');
  }

  /// Pulse-flavoured matches list (S3 supersedes the legacy `getMatches()`).
  /// `status` is one of `all | active | quiet | sparks-waiting`.
  Future<List<MatchSummary>> getPulseMatches({String status = 'all'}) async {
    final response = await _api.get(
      '/v1/dating/matches',
      queryParameters: {'status': status},
    );
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map(
          (item) => MatchSummary.fromJson(Map<String, dynamic>.from(item)),
        )
        .toList();
  }

  Future<MatchDetail?> getMatch(String id) async {
    try {
      final response = await _api.get('/v1/dating/matches/$id');
      final data = _unwrapData(response.data);
      if (data is Map<String, dynamic>) {
        return MatchDetail.fromJson(data);
      }
      if (data is Map) {
        return MatchDetail.fromJson(Map<String, dynamic>.from(data));
      }
      return null;
    } on DioException catch (error) {
      if (error.response?.statusCode == 404) return null;
      rethrow;
    }
  }

  Future<void> closeMatch(String id) async {
    await _api.post('/v1/dating/matches/$id/close');
  }

  /// Premium: extend a match's expiry. Surfaces the gating error from the
  /// backend (HTTP 402) so the UI can show the upsell.
  Future<void> extendMatch(String id) async {
    await _api.post('/v1/dating/matches/$id/extend');
  }

  // ---------------------------------------------------------------------
  // Sprint 4 — Verification (Aadhaar/DigiLocker + selfie liveness).
  //
  //   POST /v1/dating/verification/aadhaar/start
  //   POST /v1/dating/verification/aadhaar/callback { code, state }
  //   POST /v1/dating/verification/selfie           { embedding: [float] }
  //
  // DPDP: we never POST the Aadhaar number itself. The DigiLocker code
  // exchange is brokered server-side; the only thing this client sees is
  // an opaque OAuth state string. See PULSE_DATING_SPEC §15.
  // ---------------------------------------------------------------------

  /// Kick off DigiLocker OAuth. The backend mints a `state`, returns the
  /// `digilocker_authorize_url` we navigate the user to.
  Future<AadhaarFlowStart> startAadhaarVerification() async {
    final response = await _api.post('/v1/dating/verification/aadhaar/start');
    return AadhaarFlowStart.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  /// After redirect: exchange the OAuth code+state for verification.
  Future<AadhaarFlowResult> completeAadhaarVerification({
    required String code,
    required String state,
  }) async {
    try {
      final response = await _api.post(
        '/v1/dating/verification/aadhaar/callback',
        data: {'code': code, 'state': state},
      );
      return AadhaarFlowResult.fromJson(
        Map<String, dynamic>.from(_unwrapData(response.data) as Map),
      );
    } on DioException catch (e) {
      final body = e.response?.data;
      String? msg;
      if (body is Map && body['error'] is Map) {
        msg = (body['error'] as Map)['message']?.toString();
      } else if (body is Map && body['message'] is String) {
        msg = body['message'] as String;
      }
      return AadhaarFlowResult(
        trustTier: 'none',
        success: false,
        errorMessage: msg ?? 'Aadhaar verification failed.',
      );
    }
  }

  /// Submit a selfie embedding (vector of floats) for liveness verification.
  Future<SelfieFlowResult> submitSelfieVerification(
    List<double> embedding,
  ) async {
    final response = await _api.post(
      '/v1/dating/verification/selfie',
      data: {'embedding': embedding},
    );
    return SelfieFlowResult.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  // ---------------------------------------------------------------------
  // Sprint 4 — Vouching.
  //
  //   POST   /v1/dating/vouches               { vouchee_id, relationship,
  //                                             community_id?, note }
  //   GET    /v1/dating/vouches               (FOR me)
  //   GET    /v1/dating/vouches/sent          (I sent)
  //   PATCH  /v1/dating/vouches/:id           { decision }
  //   DELETE /v1/dating/vouches/:id           (revoke)
  // ---------------------------------------------------------------------

  /// Send a vouch request. Note caps at 140 chars per spec §5.4.
  Future<Vouch> sendVouchRequest({
    required String voucheeId,
    required String relationship,
    String? communityId,
    String? note,
  }) async {
    final response = await _api.post(
      '/v1/dating/vouches',
      data: {
        'vouchee_id': voucheeId,
        'relationship': relationship,
        if (communityId != null && communityId.isNotEmpty)
          'community_id': communityId,
        if (note != null && note.isNotEmpty) 'note': note,
      },
    );
    return Vouch.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  /// Vouches FOR me. Includes pending requests (someone asked me to vouch
  /// for them) plus accepted/active vouches.
  Future<List<Vouch>> getVouchesForMe() async {
    final response = await _api.get('/v1/dating/vouches');
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map((m) => Vouch.fromJson(Map<String, dynamic>.from(m)))
        .toList();
  }

  /// Vouches I sent (asked others to vouch for me).
  Future<List<Vouch>> getVouchesSent() async {
    final response = await _api.get('/v1/dating/vouches/sent');
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map((m) => Vouch.fromJson(Map<String, dynamic>.from(m)))
        .toList();
  }

  /// Decide a pending vouch request (accept / decline).
  Future<Vouch> decideVouch(String id, String decision) async {
    final response = await _api.patch(
      '/v1/dating/vouches/$id',
      data: {'decision': decision},
    );
    return Vouch.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  /// Revoke a vouch (either side; backend enforces who can revoke).
  Future<void> revokeVouch(String id) async {
    await _api.delete('/v1/dating/vouches/$id');
  }

  // ---------------------------------------------------------------------
  // Sprint 4 — Safety center, panic, share-location, safe-meet, block,
  // report.
  //
  //   POST /v1/dating/safety/panic                    { lat?, lng? }
  //   POST /v1/dating/safety/share-location           { duration_minutes,
  //                                                     contact_id }
  //   POST /v1/dating/safety/meet                     { with_user_id, when,
  //                                                     lat, lng,
  //                                                     venue_name }
  //   POST /v1/dating/safety/meet/:id/check-in        { status }
  //   POST /v1/dating/safety/block                    { target_user_id }
  //   POST /v1/dating/safety/report                   { target_user_id,
  //                                                     category, details }
  // ---------------------------------------------------------------------

  /// Trigger the panic flow (60-min live location share + Trust & Safety
  /// notify). `lat`/`lng` may be null when GPS isn't available — backend
  /// still notifies.
  Future<void> panic({double? lat, double? lng}) async {
    await _api.post(
      '/v1/dating/safety/panic',
      data: {
        if (lat != null) 'location_lat': lat,
        if (lng != null) 'location_lng': lng,
      },
    );
  }

  /// Start sharing live location with the trusted contact.
  Future<void> shareLocation({
    required int durationMinutes,
    required String contactId,
  }) async {
    await _api.post(
      '/v1/dating/safety/share-location',
      data: {
        'duration_minutes': durationMinutes,
        'contact_id': contactId,
      },
    );
  }

  /// Schedule a safe-meet (premium gate enforced server-side; surfaces 402
  /// when the viewer is on a free plan).
  Future<SafeMeet> scheduleSafeMeet({
    required String withUserId,
    required DateTime when,
    required double lat,
    required double lng,
    required String venueName,
  }) async {
    final response = await _api.post(
      '/v1/dating/safety/meet',
      data: {
        'with_user_id': withUserId,
        'when': when.toUtc().toIso8601String(),
        'lat': lat,
        'lng': lng,
        'venue_name': venueName,
      },
    );
    return SafeMeet.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  /// Check in on a scheduled safe-meet. `status` is `'safe' | 'help'`.
  Future<void> safeMeetCheckIn({
    required String meetId,
    required String status,
  }) async {
    await _api.post(
      '/v1/dating/safety/meet/$meetId/check-in',
      data: {'status': status},
    );
  }

  /// Block a user. Cuts both directions of the dating graph immediately.
  Future<void> blockUser(String targetUserId) async {
    await _api.post(
      '/v1/dating/safety/block',
      data: {'target_user_id': targetUserId},
    );
  }

  /// File a report. `category` is a free-form spec-defined enum
  /// (harassment | inappropriate_photo | impersonation | spam | other).
  Future<void> reportUser({
    required String targetUserId,
    required String category,
    String? details,
  }) async {
    await _api.post(
      '/v1/dating/safety/report',
      data: {
        'target_user_id': targetUserId,
        'category': category,
        if (details != null && details.isNotEmpty) 'details': details,
      },
    );
  }

  /// List of reports the viewer has filed plus their current status. Phase 1
  /// addition — calls `GET /v1/dating/safety/reports/me`. If the backend
  /// hasn't surfaced the endpoint yet, returns a sentinel that the UI
  /// renders as "endpoint pending".
  ///
  /// Returns a `MyReportsResult` so the UI can distinguish:
  ///   - empty list + endpoint pending (HTTP 404/501/unknown route)
  ///   - empty list + endpoint working (user simply has no reports)
  ///   - non-empty list
  Future<MyReportsResult> getMyReports() async {
    try {
      final response = await _api.get('/v1/dating/safety/reports/me');
      final data = _unwrapData(response.data);
      if (data is! List) {
        return const MyReportsResult(items: [], endpointAvailable: true);
      }
      final items = data
          .whereType<Map>()
          .map(
            (item) => MyReportEntry.fromJson(Map<String, dynamic>.from(item)),
          )
          .toList();
      return MyReportsResult(items: items, endpointAvailable: true);
    } on DioException catch (e) {
      // 404/501 = backend hasn't shipped the endpoint yet. Any other
      // status is a real error and we rethrow so the UI can show its
      // error state.
      final code = e.response?.statusCode;
      if (code == 404 || code == 501) {
        return const MyReportsResult(items: [], endpointAvailable: false);
      }
      rethrow;
    }
  }

  // ---------------------------------------------------------------------
  // Sprint 5 — Premium tier + DPDP data export.
  //
  //   GET    /v1/dating/premium/plans
  //   POST   /v1/dating/premium/checkout    { plan_id, source }
  //   GET    /v1/dating/premium/me
  //   POST   /v1/dating/premium/cancel
  //   POST   /v1/dating/pulse/boost
  //   POST   /v1/dating/matches/:id/extend  (already wired above)
  //   GET    /v1/dating/data-export/me
  //   POST   /v1/dating/data-export
  // ---------------------------------------------------------------------

  /// List of monetised plans the user can subscribe to (or the one-shot
  /// `boost_49` token plan). Sorted server-side by recommended order.
  Future<List<PremiumPlan>> getPremiumPlans() async {
    final response = await _api.get('/v1/dating/premium/plans');
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map(
          (item) => PremiumPlan.fromJson(Map<String, dynamic>.from(item)),
        )
        .toList();
  }

  /// Start a Razorpay checkout for the chosen plan. `source` is a free-form
  /// short string the analytics service uses to attribute conversions
  /// (e.g. `'paywall:boost'`, `'premium_screen'`).
  Future<PremiumCheckoutOrder> startCheckout({
    required String planId,
    required String source,
  }) async {
    final response = await _api.post(
      '/v1/dating/premium/checkout',
      data: {'plan_id': planId, 'source': source},
    );
    return PremiumCheckoutOrder.fromJson(
      Map<String, dynamic>.from(_unwrapData(response.data) as Map),
    );
  }

  /// Current premium state for the viewer. Returns the canonical free state
  /// for un-subscribed users (active=false).
  Future<PremiumState> getPremium() async {
    try {
      final response = await _api.get('/v1/dating/premium/me');
      final data = _unwrapData(response.data);
      if (data is Map<String, dynamic>) {
        return PremiumState.fromJson(data);
      }
      if (data is Map) {
        return PremiumState.fromJson(Map<String, dynamic>.from(data));
      }
      return PremiumState.free;
    } on DioException catch (error) {
      if (error.response?.statusCode == 404) return PremiumState.free;
      rethrow;
    }
  }

  /// Cancel auto-renew. The current period continues until `expires_at`.
  Future<void> cancelPremium() async {
    await _api.post('/v1/dating/premium/cancel');
  }

  /// Use one Pulse Boost (premium daily quota or one-shot boost_49 token).
  Future<void> useBoost() async {
    await _api.post('/v1/dating/pulse/boost');
  }

  /// Request a fresh DPDP data export. Backend rate-limits to 1/7d.
  Future<DataExportRecord?> requestDataExport() async {
    final response = await _api.post('/v1/dating/data-export');
    final data = _unwrapData(response.data);
    if (data is Map<String, dynamic>) {
      return DataExportRecord.fromJson(data);
    }
    if (data is Map) {
      return DataExportRecord.fromJson(Map<String, dynamic>.from(data));
    }
    return null;
  }

  // ---------------------------------------------------------------------
  // Sprint 6 — soft-launch waitlist + cohort gate.
  //
  // `joinWaitlist` posts to a stub endpoint. Backend wires this in Sprint 7
  // (issue: PULSE-901). Until then the call is a fire-and-forget POST that
  // should not surface a hard error — the city-gate UI swallows network
  // failures and shows a friendly retry.
  // ---------------------------------------------------------------------

  /// Join the city waitlist. Called from `PulseCityGatedScreen`.
  Future<void> joinWaitlist({
    required String city,
    required String email,
  }) async {
    // TODO(S7): backend will implement POST /v1/dating/waitlist. Until
    // then this is a no-op against a stub endpoint that mirrors the
    // expected request shape.
    try {
      await _api.post(
        '/v1/dating/waitlist',
        data: {
          'city': city,
          'email': email,
        },
      );
    } on DioException catch (e) {
      // 404 / 501 — endpoint not yet wired; treat as success so the UI
      // can show the confirmation card. Any other error bubbles up so
      // the screen can show a retry prompt.
      final code = e.response?.statusCode;
      if (code == 404 || code == 501 || code == null) return;
      rethrow;
    }
  }

  /// History of past data exports plus any in-progress one.
  Future<List<DataExportRecord>> getDataExports() async {
    final response = await _api.get('/v1/dating/data-export/me');
    final data = _unwrapData(response.data);
    if (data is! List) return const [];
    return data
        .whereType<Map>()
        .map(
          (item) =>
              DataExportRecord.fromJson(Map<String, dynamic>.from(item)),
        )
        .toList();
  }

  dynamic _unwrapData(dynamic body) {
    if (body is Map<String, dynamic> && body.containsKey('data')) {
      return body['data'];
    }
    return body;
  }
}

final pulseRepositoryProvider = Provider<PulseRepository>((ref) {
  return PulseRepository(
    ref.watch(pulseApiClientProvider),
    ref.watch(pulseAuthServiceProvider),
  );
});
