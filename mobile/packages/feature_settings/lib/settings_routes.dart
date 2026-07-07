import 'package:feature_settings/data_saver_screen.dart';
import 'package:feature_settings/edit_profile_screen.dart';
import 'package:feature_settings/notification_settings_screen.dart';
import 'package:feature_settings/privacy_settings_screen.dart';
import 'package:feature_settings/security_settings_screen.dart';
import 'package:feature_settings/settings_screen.dart';
import 'package:feature_settings/verification_screen.dart';
import 'package:feature_settings/wellbeing_settings_screen.dart';
import 'package:go_router/go_router.dart';

/// Settings route table (root + profile/security/notifications/privacy/
/// wellbeing/data-saver/verification). Spread into the app router's shell.
List<RouteBase> settingsRoutes() => [
  GoRoute(path: '/settings', builder: (_, _) => const SettingsScreen()),
  GoRoute(
      path: '/settings/profile', builder: (_, _) => const EditProfileScreen()),
  GoRoute(
      path: '/settings/security',
      builder: (_, _) => const SecuritySettingsScreen()),
  GoRoute(
      path: '/settings/notifications',
      builder: (_, _) => const NotificationSettingsScreen()),
  GoRoute(
      path: '/settings/privacy',
      builder: (_, _) => const PrivacySettingsScreen()),
  GoRoute(
      path: '/settings/wellbeing',
      builder: (_, _) => const WellbeingSettingsScreen()),
  GoRoute(
      path: '/settings/data-saver', builder: (_, _) => const DataSaverScreen()),
  GoRoute(
      path: '/settings/verification',
      builder: (_, _) => const VerificationScreen()),
];
