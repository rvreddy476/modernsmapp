class Slambook {
  final String id;
  final String ownerUserId;
  final String contextType;
  final String? contextId;
  final String title;
  final String? subtitle;
  final String? description;
  final String category;
  final String themeKey;
  final String? coverMediaId;
  final String visibility;
  final String responseIdentityMode;
  final bool approvalRequired;
  final bool allowCustomCards;
  final bool allowReactions;
  final bool allowComments;
  final bool allowShareLink;
  final int maxResponsesPerUser;
  final DateTime opensAt;
  final DateTime? closesAt;
  final String status;
  final int invitedCount;
  final int responseCount;
  final int approvedCount;
  final int pinnedCount;
  final DateTime lastActivityAt;
  final DateTime createdAt;
  final DateTime updatedAt;
  final DateTime? deletedAt;
  final String? viewerResponseStatus;
  final String? viewerSessionId;
  final bool viewerCanRespond;
  final bool viewerCanModerate;
  final String? shareToken;

  const Slambook({
    required this.id,
    required this.ownerUserId,
    required this.contextType,
    this.contextId,
    required this.title,
    this.subtitle,
    this.description,
    required this.category,
    required this.themeKey,
    this.coverMediaId,
    required this.visibility,
    required this.responseIdentityMode,
    required this.approvalRequired,
    required this.allowCustomCards,
    required this.allowReactions,
    required this.allowComments,
    required this.allowShareLink,
    required this.maxResponsesPerUser,
    required this.opensAt,
    this.closesAt,
    required this.status,
    required this.invitedCount,
    required this.responseCount,
    required this.approvedCount,
    required this.pinnedCount,
    required this.lastActivityAt,
    required this.createdAt,
    required this.updatedAt,
    this.deletedAt,
    this.viewerResponseStatus,
    this.viewerSessionId,
    this.viewerCanRespond = false,
    this.viewerCanModerate = false,
    this.shareToken,
  });

  factory Slambook.fromJson(Map<String, dynamic> json) {
    return Slambook(
      id: json['id'] as String? ?? '',
      ownerUserId: json['owner_user_id'] as String? ?? '',
      contextType: json['context_type'] as String? ?? '',
      contextId: json['context_id'] as String?,
      title: json['title'] as String? ?? '',
      subtitle: json['subtitle'] as String?,
      description: json['description'] as String?,
      category: json['category'] as String? ?? 'personal',
      themeKey: json['theme_key'] as String? ?? 'classic',
      coverMediaId: json['cover_media_id'] as String?,
      visibility: json['visibility'] as String? ?? 'invited_only',
      responseIdentityMode: json['response_identity_mode'] as String? ?? 'named',
      approvalRequired: json['approval_required'] as bool? ?? false,
      allowCustomCards: json['allow_custom_cards'] as bool? ?? false,
      allowReactions: json['allow_reactions'] as bool? ?? false,
      allowComments: json['allow_comments'] as bool? ?? false,
      allowShareLink: json['allow_share_link'] as bool? ?? false,
      maxResponsesPerUser: json['max_responses_per_user'] as int? ?? 0,
      opensAt: _parseDateTime(json['opens_at']) ?? DateTime.now(),
      closesAt: _parseDateTime(json['closes_at']),
      status: json['status'] as String? ?? 'active',
      invitedCount: json['invited_count'] as int? ?? 0,
      responseCount: json['response_count'] as int? ?? 0,
      approvedCount: json['approved_count'] as int? ?? 0,
      pinnedCount: json['pinned_count'] as int? ?? 0,
      lastActivityAt: _parseDateTime(json['last_activity_at']) ?? DateTime.now(),
      createdAt: _parseDateTime(json['created_at']) ?? DateTime.now(),
      updatedAt: _parseDateTime(json['updated_at']) ?? DateTime.now(),
      deletedAt: _parseDateTime(json['deleted_at']),
      viewerResponseStatus: json['viewer_response_status'] as String?,
      viewerSessionId: json['viewer_session_id'] as String?,
      viewerCanRespond: json['viewer_can_respond'] as bool? ?? false,
      viewerCanModerate: json['viewer_can_moderate'] as bool? ?? false,
      shareToken: json['share_token'] as String?,
    );
  }
}

class SlambookTemplatePack {
  final String id;
  final String key;
  final String title;
  final String? description;
  final String category;
  final List<SlambookTemplate> templates;

  const SlambookTemplatePack({
    required this.id,
    required this.key,
    required this.title,
    this.description,
    required this.category,
    required this.templates,
  });

  factory SlambookTemplatePack.fromJson(Map<String, dynamic> json) {
    final templates = (json['templates'] as List<dynamic>? ?? const <dynamic>[])
        .map((item) => SlambookTemplate.fromJson(item as Map<String, dynamic>))
        .toList();
    return SlambookTemplatePack(
      id: json['id'] as String? ?? '',
      key: json['key'] as String? ?? '',
      title: json['title'] as String? ?? '',
      description: json['description'] as String?,
      category: json['category'] as String? ?? 'general',
      templates: templates,
    );
  }
}

class SlambookTemplate {
  final String id;
  final String packId;
  final String title;
  final String prompt;
  final String responseType;
  final String? placeholderText;
  final String? helpText;
  final Map<String, dynamic> config;
  final int orderIndex;

  const SlambookTemplate({
    required this.id,
    required this.packId,
    required this.title,
    required this.prompt,
    required this.responseType,
    this.placeholderText,
    this.helpText,
    required this.config,
    required this.orderIndex,
  });

  factory SlambookTemplate.fromJson(Map<String, dynamic> json) {
    return SlambookTemplate(
      id: json['id'] as String? ?? '',
      packId: json['pack_id'] as String? ?? '',
      title: json['title'] as String? ?? '',
      prompt: json['prompt'] as String? ?? '',
      responseType: json['response_type'] as String? ?? 'text',
      placeholderText: json['placeholder_text'] as String?,
      helpText: json['help_text'] as String?,
      config: _asMap(json['config']),
      orderIndex: json['order_index'] as int? ?? 0,
    );
  }
}

class SlambookCardDraft {
  final String title;
  final String prompt;
  final String responseType;
  final String? placeholderText;
  final String? helpText;
  final bool isRequired;

  const SlambookCardDraft({
    required this.title,
    required this.prompt,
    required this.responseType,
    this.placeholderText,
    this.helpText,
    this.isRequired = false,
  });

  Map<String, dynamic> toJson() {
    return {
      'title': title,
      'prompt': prompt,
      'response_type': responseType,
      'placeholder_text': placeholderText,
      'help_text': helpText,
      'is_required': isRequired,
    };
  }
}

class SlambookCard {
  final String id;
  final String slambookId;
  final String sourceType;
  final String? templateId;
  final String title;
  final String prompt;
  final String responseType;
  final String? placeholderText;
  final String? helpText;
  final Map<String, dynamic> config;
  final bool isRequired;
  final bool isActive;
  final bool lockedAfterResponse;
  final int orderIndex;
  final int versionNo;
  final String createdByUserId;
  final DateTime createdAt;
  final DateTime updatedAt;
  final DateTime? deletedAt;
  final List<SlambookCardOption> options;

  const SlambookCard({
    required this.id,
    required this.slambookId,
    required this.sourceType,
    this.templateId,
    required this.title,
    required this.prompt,
    required this.responseType,
    this.placeholderText,
    this.helpText,
    required this.config,
    required this.isRequired,
    required this.isActive,
    required this.lockedAfterResponse,
    required this.orderIndex,
    required this.versionNo,
    required this.createdByUserId,
    required this.createdAt,
    required this.updatedAt,
    this.deletedAt,
    required this.options,
  });

  factory SlambookCard.fromJson(Map<String, dynamic> json) {
    return SlambookCard(
      id: json['id'] as String? ?? '',
      slambookId: json['slambook_id'] as String? ?? '',
      sourceType: json['source_type'] as String? ?? 'custom',
      templateId: json['template_id'] as String?,
      title: json['title'] as String? ?? '',
      prompt: json['prompt'] as String? ?? '',
      responseType: json['response_type'] as String? ?? 'text',
      placeholderText: json['placeholder_text'] as String?,
      helpText: json['help_text'] as String?,
      config: _asMap(json['config']),
      isRequired: json['is_required'] as bool? ?? false,
      isActive: json['is_active'] as bool? ?? true,
      lockedAfterResponse: json['locked_after_response'] as bool? ?? false,
      orderIndex: json['order_index'] as int? ?? 0,
      versionNo: json['version_no'] as int? ?? 1,
      createdByUserId: json['created_by_user_id'] as String? ?? '',
      createdAt: _parseDateTime(json['created_at']) ?? DateTime.now(),
      updatedAt: _parseDateTime(json['updated_at']) ?? DateTime.now(),
      deletedAt: _parseDateTime(json['deleted_at']),
      options: (json['options'] as List<dynamic>? ?? const <dynamic>[])
          .map((item) => SlambookCardOption.fromJson(item as Map<String, dynamic>))
          .toList(),
    );
  }
}

class SlambookCardOption {
  final String id;
  final String cardId;
  final String label;
  final String value;
  final int orderIndex;

  const SlambookCardOption({
    required this.id,
    required this.cardId,
    required this.label,
    required this.value,
    required this.orderIndex,
  });

  factory SlambookCardOption.fromJson(Map<String, dynamic> json) {
    return SlambookCardOption(
      id: json['id'] as String? ?? '',
      cardId: json['card_id'] as String? ?? '',
      label: json['label'] as String? ?? '',
      value: json['value'] as String? ?? '',
      orderIndex: json['order_index'] as int? ?? 0,
    );
  }
}

class SlambookInvite {
  final String id;
  final String slambookId;
  final String inviterUserId;
  final String inviteType;
  final String? targetUserId;
  final String? targetEmail;
  final String? targetRefId;
  final String? shareToken;
  final String? message;
  final String status;
  final DateTime? openedAt;
  final DateTime? acceptedAt;
  final DateTime? declinedAt;
  final DateTime? expiresAt;
  final DateTime createdAt;
  final DateTime updatedAt;

  const SlambookInvite({
    required this.id,
    required this.slambookId,
    required this.inviterUserId,
    required this.inviteType,
    this.targetUserId,
    this.targetEmail,
    this.targetRefId,
    this.shareToken,
    this.message,
    required this.status,
    this.openedAt,
    this.acceptedAt,
    this.declinedAt,
    this.expiresAt,
    required this.createdAt,
    required this.updatedAt,
  });

  factory SlambookInvite.fromJson(Map<String, dynamic> json) {
    return SlambookInvite(
      id: json['id'] as String? ?? '',
      slambookId: json['slambook_id'] as String? ?? '',
      inviterUserId: json['inviter_user_id'] as String? ?? '',
      inviteType: json['invite_type'] as String? ?? 'user',
      targetUserId: json['target_user_id'] as String?,
      targetEmail: json['target_email'] as String?,
      targetRefId: json['target_ref_id'] as String?,
      shareToken: json['share_token'] as String?,
      message: json['message'] as String?,
      status: json['status'] as String? ?? 'pending',
      openedAt: _parseDateTime(json['opened_at']),
      acceptedAt: _parseDateTime(json['accepted_at']),
      declinedAt: _parseDateTime(json['declined_at']),
      expiresAt: _parseDateTime(json['expires_at']),
      createdAt: _parseDateTime(json['created_at']) ?? DateTime.now(),
      updatedAt: _parseDateTime(json['updated_at']) ?? DateTime.now(),
    );
  }
}

class SlambookResponseAnswerDraft {
  final String cardId;
  final String answerText;
  final Map<String, dynamic> answerJson;

  const SlambookResponseAnswerDraft({
    required this.cardId,
    this.answerText = '',
    this.answerJson = const {},
  });

  Map<String, dynamic> toJson() {
    return {
      'card_id': cardId,
      'answer_text': answerText,
      'answer_json': answerJson,
    };
  }
}

class SlambookResponseItem {
  final String id;
  final String sessionId;
  final String slambookId;
  final String cardId;
  final String responseType;
  final String? answerText;
  final Map<String, dynamic> answerJson;
  final String? mediaAssetId;
  final String? cardTitle;
  final String? cardPrompt;
  final DateTime createdAt;
  final DateTime updatedAt;

  const SlambookResponseItem({
    required this.id,
    required this.sessionId,
    required this.slambookId,
    required this.cardId,
    required this.responseType,
    this.answerText,
    required this.answerJson,
    this.mediaAssetId,
    this.cardTitle,
    this.cardPrompt,
    required this.createdAt,
    required this.updatedAt,
  });

  factory SlambookResponseItem.fromJson(Map<String, dynamic> json) {
    return SlambookResponseItem(
      id: json['id'] as String? ?? '',
      sessionId: json['session_id'] as String? ?? '',
      slambookId: json['slambook_id'] as String? ?? '',
      cardId: json['card_id'] as String? ?? '',
      responseType: json['response_type'] as String? ?? 'text',
      answerText: json['answer_text'] as String?,
      answerJson: _asMap(json['answer_json']),
      mediaAssetId: json['media_asset_id'] as String?,
      cardTitle: json['card_title'] as String?,
      cardPrompt: json['card_prompt'] as String?,
      createdAt: _parseDateTime(json['created_at']) ?? DateTime.now(),
      updatedAt: _parseDateTime(json['updated_at']) ?? DateTime.now(),
    );
  }
}

class SlambookResponseSession {
  final String id;
  final String slambookId;
  final String? inviteId;
  final String? responderUserId;
  final String? displayName;
  final String identityMode;
  final String status;
  final DateTime startedAt;
  final DateTime? draftLastSavedAt;
  final DateTime? submittedAt;
  final DateTime? moderatedAt;
  final String? moderatedByUserId;
  final String? moderationReason;
  final DateTime createdAt;
  final DateTime updatedAt;
  final List<SlambookResponseItem> items;

  const SlambookResponseSession({
    required this.id,
    required this.slambookId,
    this.inviteId,
    this.responderUserId,
    this.displayName,
    required this.identityMode,
    required this.status,
    required this.startedAt,
    this.draftLastSavedAt,
    this.submittedAt,
    this.moderatedAt,
    this.moderatedByUserId,
    this.moderationReason,
    required this.createdAt,
    required this.updatedAt,
    required this.items,
  });

  factory SlambookResponseSession.fromJson(Map<String, dynamic> json) {
    return SlambookResponseSession(
      id: json['id'] as String? ?? '',
      slambookId: json['slambook_id'] as String? ?? '',
      inviteId: json['invite_id'] as String?,
      responderUserId: json['responder_user_id'] as String?,
      displayName: json['display_name'] as String? ?? json['display_name_snapshot'] as String?,
      identityMode: json['identity_mode'] as String? ?? 'named',
      status: json['status'] as String? ?? 'draft',
      startedAt: _parseDateTime(json['started_at']) ?? DateTime.now(),
      draftLastSavedAt: _parseDateTime(json['draft_last_saved_at']),
      submittedAt: _parseDateTime(json['submitted_at']),
      moderatedAt: _parseDateTime(json['moderated_at']),
      moderatedByUserId: json['moderated_by_user_id'] as String?,
      moderationReason: json['moderation_reason'] as String?,
      createdAt: _parseDateTime(json['created_at']) ?? DateTime.now(),
      updatedAt: _parseDateTime(json['updated_at']) ?? DateTime.now(),
      items: (json['items'] as List<dynamic>? ?? const <dynamic>[])
          .map((item) => SlambookResponseItem.fromJson(item as Map<String, dynamic>))
          .toList(),
    );
  }
}

class SlambookOpinionSpaceItem {
  final String id;
  final String slambookId;
  final String sessionId;
  final String responseItemId;
  final String status;
  final bool isPinned;
  final String? boardSection;
  final double boardOrder;
  final int zIndex;
  final String? featuredBadge;
  final String? ownerNote;
  final DateTime? approvedAt;
  final String? hiddenReason;
  final DateTime createdAt;
  final DateTime updatedAt;
  final String? responderDisplayName;
  final bool anonymous;
  final String cardTitle;
  final String cardPrompt;
  final String responseType;
  final String? answerText;
  final Map<String, dynamic> answerJson;

  const SlambookOpinionSpaceItem({
    required this.id,
    required this.slambookId,
    required this.sessionId,
    required this.responseItemId,
    required this.status,
    required this.isPinned,
    this.boardSection,
    required this.boardOrder,
    required this.zIndex,
    this.featuredBadge,
    this.ownerNote,
    this.approvedAt,
    this.hiddenReason,
    required this.createdAt,
    required this.updatedAt,
    this.responderDisplayName,
    required this.anonymous,
    required this.cardTitle,
    required this.cardPrompt,
    required this.responseType,
    this.answerText,
    required this.answerJson,
  });

  factory SlambookOpinionSpaceItem.fromJson(Map<String, dynamic> json) {
    return SlambookOpinionSpaceItem(
      id: json['id'] as String? ?? '',
      slambookId: json['slambook_id'] as String? ?? '',
      sessionId: json['session_id'] as String? ?? '',
      responseItemId: json['response_item_id'] as String? ?? '',
      status: json['status'] as String? ?? 'pending',
      isPinned: json['is_pinned'] as bool? ?? false,
      boardSection: json['board_section'] as String?,
      boardOrder: (json['board_order'] as num?)?.toDouble() ?? 0,
      zIndex: json['z_index'] as int? ?? 0,
      featuredBadge: json['featured_badge'] as String?,
      ownerNote: json['owner_note'] as String?,
      approvedAt: _parseDateTime(json['approved_at']),
      hiddenReason: json['hidden_reason'] as String?,
      createdAt: _parseDateTime(json['created_at']) ?? DateTime.now(),
      updatedAt: _parseDateTime(json['updated_at']) ?? DateTime.now(),
      responderDisplayName: json['responder_display_name'] as String?,
      anonymous: json['anonymous'] as bool? ?? false,
      cardTitle: json['card_title'] as String? ?? '',
      cardPrompt: json['card_prompt'] as String? ?? '',
      responseType: json['response_type'] as String? ?? 'text',
      answerText: json['answer_text'] as String?,
      answerJson: _asMap(json['answer_json']),
    );
  }
}

class SlambookDetail {
  final Slambook slambook;
  final List<SlambookCard> cards;
  final SlambookResponseSession? viewerSession;

  const SlambookDetail({
    required this.slambook,
    required this.cards,
    this.viewerSession,
  });

  factory SlambookDetail.fromJson(Map<String, dynamic> json) {
    return SlambookDetail(
      slambook: Slambook.fromJson(json['slambook'] as Map<String, dynamic>? ?? const {}),
      cards: (json['cards'] as List<dynamic>? ?? const <dynamic>[])
          .map((item) => SlambookCard.fromJson(item as Map<String, dynamic>))
          .toList(),
      viewerSession: json['viewer_session'] != null
          ? SlambookResponseSession.fromJson(json['viewer_session'] as Map<String, dynamic>)
          : null,
    );
  }
}

Map<String, dynamic> _asMap(dynamic value) {
  if (value is Map<String, dynamic>) {
    return value;
  }
  if (value is Map) {
    return value.map((key, dynamic item) => MapEntry(key.toString(), item));
  }
  return const {};
}

DateTime? _parseDateTime(dynamic value) {
  if (value is String) {
    return DateTime.tryParse(value)?.toLocal();
  }
  return null;
}
