/// Environment configuration for API endpoints.
class Environment {
  const Environment._();

  // Base URLs — change these per environment (dev/staging/prod)
  static const String apiBaseUrl = 'http://10.0.2.2:8080'; // Android emulator → host
  static const String wsBaseUrl = 'ws://10.0.2.2:8092';
  static const String wsGatewayUrl = 'ws://10.0.2.2:8089';

  // API paths
  static const String authPath = '/v1/auth';
  static const String usersPath = '/v1/users';
  static const String profilesPath = '/v1/profiles';
  static const String postsPath = '/v1/posts';
  static const String feedPath = '/v1/feed';
  static const String mediaPath = '/v1/media';
  static const String notificationsPath = '/v1/notifications';
  static const String chatPath = '/v1/chat';
  static const String graphPath = '/v1/graph';
  static const String searchPath = '/v1/search';
  static const String suggestionsPath = '/v1/suggestions';
  static const String analyticsPath = '/v1/analytics';
  static const String shopPath = '/v1/shop';
  static const String memoriesPath = '/v1/memories';
  static const String livePath = '/v1/live';
}
