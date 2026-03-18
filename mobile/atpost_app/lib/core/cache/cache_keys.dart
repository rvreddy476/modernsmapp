/// Constants for cache box names and TTL durations.
class CacheKeys {
  const CacheKeys._();

  // Box names
  static const feedBox = 'feed_cache';
  static const profileBox = 'profile_cache';
  static const conversationsBox = 'conversations_cache';

  // TTL durations
  static const feedTtl = Duration(minutes: 5);
  static const profileTtl = Duration(minutes: 10);
  static const conversationsTtl = Duration(minutes: 2);

  // Key builders
  static String feedKey(String feedMode) => 'feed_home_$feedMode';
  static const currentUserKey = 'user_me';
  static String userKey(String userId) => 'user_$userId';
  static const conversationsKey = 'conversations';
}
