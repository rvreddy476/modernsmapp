import 'package:atpost_app/data/models/conversation.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/user.dart';

/// Creates a fake [Post] with sensible defaults for testing.
Post fakePost({
  String id = 'post-1',
  String authorId = 'user-1',
  String? authorName = 'Test Author',
  String? authorAvatar,
  String content = 'Test post content',
  String contentType = 'post',
  List<String> tags = const [],
  List<String> mediaIds = const [],
  int likeCount = 0,
  int commentCount = 0,
  int shareCount = 0,
  bool isLiked = false,
  bool isBookmarked = false,
  DateTime? createdAt,
}) {
  return Post(
    id: id,
    authorId: authorId,
    authorName: authorName,
    authorAvatar: authorAvatar,
    content: content,
    contentType: contentType,
    tags: tags,
    mediaIds: mediaIds,
    likeCount: likeCount,
    commentCount: commentCount,
    shareCount: shareCount,
    isLiked: isLiked,
    isBookmarked: isBookmarked,
    createdAt: createdAt ?? DateTime(2026, 3, 16),
  );
}

/// Creates a fake [User] with sensible defaults for testing.
User fakeUser({
  String id = 'user-1',
  String username = 'testuser',
  String displayName = 'Test User',
  String? bio,
  String? avatarMediaId,
  int followerCount = 100,
  int followingCount = 50,
  int friendCount = 25,
  bool isVerified = false,
}) {
  return User(
    id: id,
    username: username,
    displayName: displayName,
    bio: bio,
    avatarMediaId: avatarMediaId,
    followerCount: followerCount,
    followingCount: followingCount,
    friendCount: friendCount,
    isVerified: isVerified,
  );
}

/// Creates a fake [Conversation] for testing.
Conversation fakeConversation({
  String id = 'conv-1',
  String type = 'direct',
  String? name,
  List<String> participantIds = const ['user-1', 'user-2'],
  String? lastMessage = 'Hey there!',
  DateTime? lastMessageAt,
  int unreadCount = 0,
}) {
  return Conversation(
    id: id,
    type: type,
    name: name,
    participantIds: participantIds,
    lastMessage: lastMessage,
    lastMessageAt: lastMessageAt ?? DateTime(2026, 3, 16),
    unreadCount: unreadCount,
  );
}

/// Creates a list of fake posts for testing.
List<Post> fakePosts({int count = 5}) {
  return List.generate(
    count,
    (i) => fakePost(
      id: 'post-$i',
      authorId: 'user-$i',
      authorName: 'Author $i',
      content: 'Post content $i',
      likeCount: i * 10,
      commentCount: i * 3,
    ),
  );
}

/// Creates a list of fake users for testing.
List<User> fakeUsers({int count = 5}) {
  return List.generate(
    count,
    (i) => fakeUser(
      id: 'user-$i',
      username: 'user$i',
      displayName: 'User $i',
      followerCount: i * 100,
    ),
  );
}
