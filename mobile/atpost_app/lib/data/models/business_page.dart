/// Mirrors user-service `business_pages` + the GET /v1/pages/:slug envelope
/// (actions / actionButtons / displayType / viewerRole). Follow-Only Pages.
class PageActions {
  final bool canFollow;
  final bool canUnfollow;
  final bool canManage;
  final bool canMessage;
  final bool canAddFriend; // always false on a page
  final bool canEdit;
  final bool canUploadDocument;
  final bool canSubmitForReview;

  const PageActions({
    this.canFollow = false,
    this.canUnfollow = false,
    this.canManage = false,
    this.canMessage = false,
    this.canAddFriend = false,
    this.canEdit = false,
    this.canUploadDocument = false,
    this.canSubmitForReview = false,
  });

  factory PageActions.fromJson(Map<String, dynamic>? j) {
    j ??= const {};
    bool b(String k) => j![k] as bool? ?? false;
    return PageActions(
      canFollow: b('canFollow'),
      canUnfollow: b('canUnfollow'),
      canManage: b('canManage'),
      canMessage: b('canMessage'),
      canAddFriend: b('canAddFriend'),
      canEdit: b('canEdit'),
      canUploadDocument: b('canUploadDocument'),
      canSubmitForReview: b('canSubmitForReview'),
    );
  }
}

class PageActionButton {
  final String id;
  final String label;
  final bool primary;
  final bool gated;

  const PageActionButton({
    required this.id,
    required this.label,
    this.primary = false,
    this.gated = false,
  });

  factory PageActionButton.fromJson(Map<String, dynamic> j) => PageActionButton(
        id: j['id'] as String? ?? '',
        label: j['label'] as String? ?? '',
        primary: j['primary'] as bool? ?? false,
        gated: j['gated'] as bool? ?? false,
      );
}

class BusinessPage {
  final String id;
  final String userId;
  final String pageHandle;
  final String pageName;
  final String pageType;
  final String category;
  final String description;
  final String? phone;
  final String? website;
  final String? coverMediaId;
  final String? avatarMediaId;
  final bool isVerified;
  final int followerCount;
  final bool? isFollowing;
  final String status;
  final String verificationStatus;
  final String? rejectionReason;
  // Envelope fields (detail GET only).
  final String? displayType;
  final String viewerRole;
  final bool isOwner;
  final String? bannerMessage;
  final PageActions actions;
  final List<PageActionButton> actionButtons;

  const BusinessPage({
    required this.id,
    required this.userId,
    required this.pageHandle,
    required this.pageName,
    required this.pageType,
    required this.category,
    required this.description,
    this.phone,
    this.website,
    this.coverMediaId,
    this.avatarMediaId,
    this.isVerified = false,
    this.followerCount = 0,
    this.isFollowing,
    this.status = 'draft',
    this.verificationStatus = 'not_submitted',
    this.rejectionReason,
    this.displayType,
    this.viewerRole = 'visitor',
    this.isOwner = false,
    this.bannerMessage,
    this.actions = const PageActions(),
    this.actionButtons = const [],
  });

  factory BusinessPage.fromJson(Map<String, dynamic> j) {
    final buttons = (j['actionButtons'] as List<dynamic>?) ?? const [];
    return BusinessPage(
      id: j['id'] as String? ?? '',
      userId: j['user_id'] as String? ?? '',
      pageHandle: j['page_handle'] as String? ?? '',
      pageName: j['page_name'] as String? ?? '',
      pageType: j['page_type'] as String? ?? '',
      category: j['category'] as String? ?? '',
      description: j['description'] as String? ?? '',
      phone: j['phone'] as String?,
      website: j['website'] as String?,
      coverMediaId: (j['cover_media_id'] as String?)?.isEmpty ?? true ? null : j['cover_media_id'] as String?,
      avatarMediaId: (j['avatar_media_id'] as String?)?.isEmpty ?? true ? null : j['avatar_media_id'] as String?,
      isVerified: j['is_verified'] as bool? ?? false,
      followerCount: (j['follower_count'] as num?)?.toInt() ?? 0,
      isFollowing: j['is_following'] as bool?,
      status: j['status'] as String? ?? 'draft',
      verificationStatus: j['verification_status'] as String? ?? 'not_submitted',
      rejectionReason: j['rejection_reason'] as String?,
      displayType: j['displayType'] as String?,
      viewerRole: j['viewerRole'] as String? ?? 'visitor',
      isOwner: j['isOwner'] as bool? ?? false,
      bannerMessage: j['bannerMessage'] as String?,
      actions: PageActions.fromJson(j['actions'] as Map<String, dynamic>?),
      actionButtons: buttons
          .whereType<Map<String, dynamic>>()
          .map(PageActionButton.fromJson)
          .toList(),
    );
  }
}

class PageDocument {
  final String id;
  final String pageId;
  final String documentType;
  final String documentUrl;
  final String status;
  final String? rejectionReason;

  const PageDocument({
    required this.id,
    required this.pageId,
    required this.documentType,
    required this.documentUrl,
    required this.status,
    this.rejectionReason,
  });

  factory PageDocument.fromJson(Map<String, dynamic> j) => PageDocument(
        id: j['id'] as String? ?? '',
        pageId: j['page_id'] as String? ?? '',
        documentType: j['document_type'] as String? ?? '',
        documentUrl: j['document_url'] as String? ?? '',
        status: j['status'] as String? ?? 'pending',
        rejectionReason: j['rejection_reason'] as String?,
      );
}
