import 'package:flutter/material.dart';

enum ServiceStatus { active, beta, comingSoon, disabled }

enum ServiceRuntimeType { internalRoute, webview, externalPartner }

enum ServicePermission {
  profile,
  location,
  camera,
  microphone,
  contacts,
  payments,
  notifications,
  mediaUpload,
  chatShare,
}

enum ServiceCategory {
  shopping,
  food,
  education,
  dating,
  jobs,
  finance,
  entertainment,
  localServices,
  social,
  qa,
}

extension ServicePermissionX on ServicePermission {
  String get key {
    switch (this) {
      case ServicePermission.profile:
        return 'profile';
      case ServicePermission.location:
        return 'location';
      case ServicePermission.camera:
        return 'camera';
      case ServicePermission.microphone:
        return 'microphone';
      case ServicePermission.contacts:
        return 'contacts';
      case ServicePermission.payments:
        return 'payments';
      case ServicePermission.notifications:
        return 'notifications';
      case ServicePermission.mediaUpload:
        return 'media_upload';
      case ServicePermission.chatShare:
        return 'chat_share';
    }
  }

  String get label {
    switch (this) {
      case ServicePermission.profile:
        return 'Your profile information';
      case ServicePermission.location:
        return 'Your location';
      case ServicePermission.camera:
        return 'Camera access';
      case ServicePermission.microphone:
        return 'Microphone access';
      case ServicePermission.contacts:
        return 'Your contacts';
      case ServicePermission.payments:
        return 'Payments and wallet';
      case ServicePermission.notifications:
        return 'Send notifications';
      case ServicePermission.mediaUpload:
        return 'Upload photos and videos';
      case ServicePermission.chatShare:
        return 'Share to chat';
    }
  }

  IconData get icon {
    switch (this) {
      case ServicePermission.profile:
        return Icons.person_rounded;
      case ServicePermission.location:
        return Icons.location_on_rounded;
      case ServicePermission.camera:
        return Icons.photo_camera_rounded;
      case ServicePermission.microphone:
        return Icons.mic_rounded;
      case ServicePermission.contacts:
        return Icons.contacts_rounded;
      case ServicePermission.payments:
        return Icons.credit_card_rounded;
      case ServicePermission.notifications:
        return Icons.notifications_rounded;
      case ServicePermission.mediaUpload:
        return Icons.upload_rounded;
      case ServicePermission.chatShare:
        return Icons.forum_rounded;
    }
  }

  static ServicePermission? fromKey(String key) {
    for (final p in ServicePermission.values) {
      if (p.key == key) return p;
    }
    return null;
  }
}

extension ServiceCategoryX on ServiceCategory {
  String get label {
    switch (this) {
      case ServiceCategory.shopping:
        return 'Shopping';
      case ServiceCategory.food:
        return 'Food';
      case ServiceCategory.education:
        return 'Education';
      case ServiceCategory.dating:
        return 'Dating';
      case ServiceCategory.jobs:
        return 'Jobs';
      case ServiceCategory.finance:
        return 'Finance';
      case ServiceCategory.entertainment:
        return 'Entertainment';
      case ServiceCategory.localServices:
        return 'Local Services';
      case ServiceCategory.social:
        return 'Social';
      case ServiceCategory.qa:
        return 'Q&A';
    }
  }
}

extension ServiceStatusX on ServiceStatus {
  String get label {
    switch (this) {
      case ServiceStatus.active:
        return 'Open';
      case ServiceStatus.beta:
        return 'Beta';
      case ServiceStatus.comingSoon:
        return 'Coming Soon';
      case ServiceStatus.disabled:
        return 'Unavailable';
    }
  }

  bool get isOpenable =>
      this == ServiceStatus.active || this == ServiceStatus.beta;
}

/// Static definition of a Postbook internal mini-app surfaced under /services.
/// Registry is hand-curated; not loaded from backend (intentional per spec).
@immutable
class ServiceApp {
  const ServiceApp({
    required this.id,
    required this.slug,
    required this.name,
    required this.shortDescription,
    this.longDescription,
    required this.iconName,
    required this.category,
    required this.status,
    required this.runtime,
    this.route,
    this.url,
    required this.permissions,
    this.isVerified = true,
    this.isInternal = true,
    this.isFeatured = false,
    required this.sortOrder,
    required this.version,
    required this.accentColor,
    this.tags = const [],
  });

  final String id;
  final String slug;
  final String name;
  final String shortDescription;
  final String? longDescription;

  /// Logical icon name (e.g. "ShoppingBag"). Resolved to an [IconData] by
  /// the rendering layer via `iconForServiceName`.
  final String iconName;

  final ServiceCategory category;
  final ServiceStatus status;
  final ServiceRuntimeType runtime;
  final String? route;
  final String? url;
  final List<ServicePermission> permissions;
  final bool isVerified;
  final bool isInternal;
  final bool isFeatured;
  final int sortOrder;
  final String version;
  final Color accentColor;
  final List<String> tags;
}
