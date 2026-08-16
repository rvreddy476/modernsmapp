import 'package:atpost_app/features/mini_apps/mini_app_detail_screen.dart';
import 'package:atpost_app/features/mini_apps/mini_app_sandbox_screen.dart';
import 'package:atpost_app/features/mini_apps/mini_apps_screen.dart';
import 'package:atpost_app/features/profile/my_media_screen.dart';
import 'package:atpost_app/features/profile/profile_detail_screen.dart';
import 'package:atpost_app/features/profile/profile_screen.dart';
import 'package:atpost_app/features/services/service_slug_router.dart';
import 'package:atpost_app/features/services/services_screen.dart';
import 'package:atpost_app/features/settings/content_preferences_screen.dart';
import 'package:atpost_app/features/settings/data_saver_screen.dart';
import 'package:atpost_app/features/settings/edit_profile_screen.dart';
import 'package:atpost_app/features/settings/notification_settings_screen.dart';
import 'package:atpost_app/features/settings/privacy_settings_screen.dart';
import 'package:atpost_app/features/settings/security_settings_screen.dart';
import 'package:atpost_app/features/settings/settings_screen.dart';
import 'package:atpost_app/features/settings/verification_screen.dart';
import 'package:atpost_app/features/settings/wellbeing_settings_screen.dart';
import 'package:atpost_app/features/pages/pages_list_screen.dart';
import 'package:atpost_app/features/pages/create_page_screen.dart';
import 'package:atpost_app/features/pages/page_detail_screen.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class SettingsRoutes {
  static List<RouteBase> get routes => [
        GoRoute(
          path: '/settings',
          builder: (context, state) => const SettingsScreen(),
        ),
        GoRoute(
          path: '/settings/profile',
          builder: (context, state) => const EditProfileScreen(),
        ),
        GoRoute(
          path: '/settings/security',
          builder: (context, state) => const SecuritySettingsScreen(),
        ),
        GoRoute(
          path: '/settings/notifications',
          builder: (context, state) => const NotificationSettingsScreen(),
        ),
        GoRoute(
          path: '/settings/privacy',
          builder: (context, state) => const PrivacySettingsScreen(),
        ),
        GoRoute(
          path: '/settings/wellbeing',
          builder: (_, _) => const WellbeingSettingsScreen(),
        ),
        GoRoute(
          path: '/settings/data-saver',
          builder: (_, _) => const DataSaverScreen(),
        ),
        GoRoute(
          path: '/settings/content-preferences',
          builder: (_, _) => const ContentPreferencesScreen(),
        ),
        GoRoute(
          path: '/settings/verification',
          builder: (_, _) => const VerificationScreen(),
        ),
        GoRoute(path: '/services', builder: (_, _) => const ServicesScreen()),
        GoRoute(
          path: '/services/:slug',
          builder: (context, state) =>
              ServiceSlugRouter(slug: state.pathParameters['slug']!),
        ),
        GoRoute(
          path: '/profile/media',
          builder: (_, _) => const MyMediaScreen(),
        ),
        GoRoute(
          path: '/profile/:userId',
          builder: (context, state) {
            final userId = state.pathParameters['userId'] ?? '';
            return Consumer(
              builder: (context, ref, _) {
                final currentUserId = ref.watch(authServiceProvider).userId;
                if (userId == currentUserId) {
                  return const ProfileScreen();
                }
                return ProfileDetailScreen(userId: userId);
              },
            );
          },
        ),
        GoRoute(path: '/apps', builder: (_, _) => const MiniAppsScreen()),
        GoRoute(
          path: '/apps/:id',
          builder: (context, state) =>
              MiniAppDetailScreen(appId: state.pathParameters['id']!),
        ),
        GoRoute(
          path: '/apps/sandbox/:id',
          builder: (context, state) =>
              MiniAppSandboxScreen(appId: state.pathParameters['id']!),
        ),
        GoRoute(
          path: '/pages',
          builder: (context, state) => const PagesListScreen(),
        ),
        GoRoute(
          path: '/pages/create',
          builder: (context, state) => const CreatePageScreen(),
        ),
        GoRoute(
          path: '/page/:handle',
          builder: (context, state) =>
              PageDetailScreen(handle: state.pathParameters['handle'] ?? ''),
        ),
      ];
}
