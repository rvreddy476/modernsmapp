import 'package:atpost_app/features/services/models/service_app.dart';
import 'package:flutter/material.dart';

/// Curated catalog of internal Postbook mini-apps surfaced under /services.
/// Hand-written; intentionally NOT loaded from backend (per spec §16 — backend
/// registry is a future phase).
///
/// Routes here are validated against actual GoRouter routes in lib/app/router.dart.
/// Existing modules: /shop, /posttube, /reels, /qa, /postmatch.
/// Coming-soon modules use the dynamic /services/:slug placeholder.
class ServiceRegistry {
  const ServiceRegistry._();

  static const List<ServiceApp> _entries = [
    ServiceApp(
      id: 'commerce',
      slug: 'commerce',
      name: 'Postbook Market',
      shortDescription: 'Buy and sell products from verified sellers.',
      longDescription:
          'Discover products from local sellers, businesses, and creators. '
          'Secure payments, verified sellers, and buyer protection built in.',
      iconName: 'ShoppingBag',
      category: ServiceCategory.shopping,
      status: ServiceStatus.active,
      runtime: ServiceRuntimeType.internalRoute,
      route: '/shop',
      permissions: [
        ServicePermission.profile,
        ServicePermission.payments,
        ServicePermission.notifications,
        ServicePermission.mediaUpload,
      ],
      isFeatured: true,
      sortOrder: 1,
      version: '1.0.0',
      accentColor: Color(0xFF6366F1),
      tags: ['shop', 'buy', 'sell', 'market', 'products'],
    ),
    ServiceApp(
      id: 'posttube',
      slug: 'posttube',
      name: 'Posttube',
      shortDescription: 'Watch and share long-form videos.',
      iconName: 'Youtube',
      category: ServiceCategory.entertainment,
      status: ServiceStatus.active,
      runtime: ServiceRuntimeType.internalRoute,
      route: '/posttube',
      permissions: [
        ServicePermission.profile,
        ServicePermission.notifications,
        ServicePermission.mediaUpload,
      ],
      isFeatured: true,
      sortOrder: 2,
      version: '1.0.0',
      accentColor: Color(0xFFEF4444),
      tags: ['video', 'watch', 'stream'],
    ),
    ServiceApp(
      id: 'flicks',
      slug: 'flicks',
      name: 'Flicks',
      shortDescription: 'Short videos, reels, and trending clips.',
      iconName: 'Clapperboard',
      category: ServiceCategory.entertainment,
      status: ServiceStatus.active,
      runtime: ServiceRuntimeType.internalRoute,
      route: '/reels',
      permissions: [
        ServicePermission.profile,
        ServicePermission.notifications,
        ServicePermission.mediaUpload,
        ServicePermission.camera,
      ],
      isFeatured: true,
      sortOrder: 3,
      version: '1.0.0',
      accentColor: Color(0xFFEC4899),
      tags: ['shorts', 'reels', 'clips', 'viral'],
    ),
    ServiceApp(
      id: 'qa',
      slug: 'qa',
      name: 'Q&A',
      shortDescription: 'Ask questions, answer, and follow topics.',
      iconName: 'HelpCircle',
      category: ServiceCategory.qa,
      status: ServiceStatus.active,
      runtime: ServiceRuntimeType.internalRoute,
      route: '/qa',
      permissions: [
        ServicePermission.profile,
        ServicePermission.notifications,
      ],
      sortOrder: 4,
      version: '1.0.0',
      accentColor: Color(0xFF0EA5E9),
      tags: ['questions', 'answers', 'knowledge', 'community'],
    ),
    ServiceApp(
      id: 'dating',
      slug: 'dating',
      name: 'Safe Dating',
      shortDescription: 'Verified and safe dating with real profiles.',
      iconName: 'Heart',
      category: ServiceCategory.dating,
      status: ServiceStatus.active,
      runtime: ServiceRuntimeType.internalRoute,
      route: '/postmatch',
      permissions: [
        ServicePermission.profile,
        ServicePermission.location,
        ServicePermission.mediaUpload,
        ServicePermission.notifications,
      ],
      sortOrder: 5,
      version: '1.0.0',
      accentColor: Color(0xFFF43F5E),
      tags: ['dating', 'match', 'connect', 'postmatch'],
    ),
    ServiceApp(
      id: 'food-home-kitchen',
      slug: 'food-home-kitchen',
      name: 'Food & Home Kitchen',
      shortDescription:
          'Order homemade food, sweets, pickles, and local meals.',
      iconName: 'UtensilsCrossed',
      category: ServiceCategory.food,
      status: ServiceStatus.comingSoon,
      runtime: ServiceRuntimeType.internalRoute,
      permissions: [
        ServicePermission.profile,
        ServicePermission.location,
        ServicePermission.payments,
        ServicePermission.notifications,
      ],
      isFeatured: true,
      sortOrder: 6,
      version: '0.1.0',
      accentColor: Color(0xFFF97316),
      tags: ['food', 'homemade', 'kitchen', 'order', 'delivery'],
    ),
    ServiceApp(
      id: 'ai-tutor',
      slug: 'ai-tutor',
      name: 'AI Tutor',
      shortDescription:
          'Learn school and college subjects with AI-powered teaching.',
      iconName: 'GraduationCap',
      category: ServiceCategory.education,
      status: ServiceStatus.comingSoon,
      runtime: ServiceRuntimeType.internalRoute,
      permissions: [
        ServicePermission.profile,
        ServicePermission.notifications,
        ServicePermission.mediaUpload,
      ],
      sortOrder: 7,
      version: '0.1.0',
      accentColor: Color(0xFF8B5CF6),
      tags: ['learn', 'study', 'tutor', 'school', 'college', 'ai'],
    ),
    ServiceApp(
      id: 'jobs',
      slug: 'jobs',
      name: 'Jobs',
      shortDescription: 'Find jobs, post openings, and hire locally.',
      iconName: 'Briefcase',
      category: ServiceCategory.jobs,
      status: ServiceStatus.comingSoon,
      runtime: ServiceRuntimeType.internalRoute,
      permissions: [
        ServicePermission.profile,
        ServicePermission.notifications,
      ],
      sortOrder: 8,
      version: '0.1.0',
      accentColor: Color(0xFF10B981),
      tags: ['jobs', 'career', 'hire', 'work'],
    ),
    ServiceApp(
      id: 'wallet',
      slug: 'wallet',
      name: 'Wallet',
      shortDescription:
          'Send money, pay for orders, and manage your balance.',
      iconName: 'Wallet',
      category: ServiceCategory.finance,
      status: ServiceStatus.comingSoon,
      runtime: ServiceRuntimeType.internalRoute,
      permissions: [
        ServicePermission.profile,
        ServicePermission.payments,
        ServicePermission.notifications,
      ],
      sortOrder: 9,
      version: '0.1.0',
      accentColor: Color(0xFFF59E0B),
      tags: ['wallet', 'money', 'pay', 'transfer', 'balance'],
    ),
  ];

  static List<ServiceApp> all() => List.unmodifiable(_entries);

  static ServiceApp? bySlug(String slug) {
    for (final app in _entries) {
      if (app.slug == slug) return app;
    }
    return null;
  }

  static ServiceApp? byId(String id) {
    for (final app in _entries) {
      if (app.id == id) return app;
    }
    return null;
  }

  static List<ServiceApp> byCategory(ServiceCategory category) {
    return _entries.where((a) => a.category == category).toList()
      ..sort((a, b) => a.sortOrder.compareTo(b.sortOrder));
  }

  /// Active or beta apps (something the user can actually open today),
  /// sorted by `sortOrder`.
  static List<ServiceApp> active() {
    return _entries.where((a) => a.status.isOpenable).toList()
      ..sort((a, b) => a.sortOrder.compareTo(b.sortOrder));
  }

  /// Featured apps that aren't disabled. Featured drives the hero section
  /// and the "explore" surface on the discovery tab.
  static List<ServiceApp> featured() {
    return _entries
        .where((a) => a.isFeatured && a.status != ServiceStatus.disabled)
        .toList()
      ..sort((a, b) => a.sortOrder.compareTo(b.sortOrder));
  }

  /// Case-insensitive substring match against name, short description, and tags.
  static List<ServiceApp> search(String query) {
    final q = query.trim().toLowerCase();
    if (q.isEmpty) return all();
    return _entries.where((a) {
      if (a.name.toLowerCase().contains(q)) return true;
      if (a.shortDescription.toLowerCase().contains(q)) return true;
      for (final tag in a.tags) {
        if (tag.toLowerCase().contains(q)) return true;
      }
      return false;
    }).toList();
  }
}
