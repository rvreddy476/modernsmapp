/// Single source of truth for atpost engagement vocabulary.
/// Backend stores generic terms. Frontend transforms at render time.
class EngagementLabels {
  EngagementLabels._();

  // Action labels
  static const String sparked = '\u2726 Sparked';
  static const String supernova = '\u2726\u2726 Supernova';
  static const String echoed = '\u21ba Echoed';
  static const String stashed = '\u229e Stashed';
  static const String commented = '\ud83d\udcac Commented';
  static const String tuned = 'Tuned'; // private, never shown

  // Count labels
  static const String sparkCount = 'Spark';
  static const String echoCount = 'Echo';
  static const String stashCount = 'Stash';
  static const String commentCount = 'Comment';

  // Action button labels
  static const String sparkAction = '\u2726 Spark';
  static const String supernovaAction = '\u2726\u2726 Supernova';
  static const String echoAction = '\u21ba Echo';
  static const String stashAction = '\u229e Stash';
  static const String tuneAction = '\u2298 Tune';

  /// Transform backend notification text to branded vocabulary.
  static String transformNotification(String text) {
    return text
        .replaceAll('liked your post', '\u2726 Sparked your post')
        .replaceAll('super liked your post', "\u2726\u2726 Supernova'd your post!")
        .replaceAll('shared your post', '\u21ba Echoed your post')
        .replaceAll('saved your post', '\u229e Stashed your post')
        .replaceAll('commented on your post', '\ud83d\udcac Commented on your post')
        .replaceAll('liked', '\u2726 Sparked')
        .replaceAll('shared', '\u21ba Echoed');
  }

  /// Format a count with its branded label.
  static String formatCount(String field, int count) {
    switch (field) {
      case 'like_count':
        return '$count ${count == 1 ? 'Spark' : 'Sparks'}';
      case 'share_count':
        return '$count ${count == 1 ? 'Echo' : 'Echoes'}';
      case 'save_count':
        return '$count ${count == 1 ? 'Stash' : 'Stashes'}';
      case 'comment_count':
        return '$count ${count == 1 ? 'Comment' : 'Comments'}';
      default:
        return '$count';
    }
  }
}
