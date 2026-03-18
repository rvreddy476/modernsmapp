import 'package:atpost_app/data/models/post.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('Post.fromJson', () {
    test('parses all fields correctly', () {
      final json = {
        'id': 'post-123',
        'author_id': 'user-456',
        'author_name': 'John Doe',
        'author_avatar': 'https://example.com/avatar.jpg',
        'content': 'Hello world!',
        'content_type': 'reel',
        'visibility': 'followers',
        'tags': ['flutter', 'dart'],
        'media_ids': ['media-1'],
        'like_count': 42,
        'comment_count': 7,
        'share_count': 3,
        'duration_seconds': 30,
        'is_liked': true,
        'is_bookmarked': false,
        'created_at': '2026-03-16T10:00:00Z',
        'feeling': 'happy',
      };

      final post = Post.fromJson(json);
      expect(post.id, 'post-123');
      expect(post.authorId, 'user-456');
      expect(post.authorName, 'John Doe');
      expect(post.content, 'Hello world!');
      expect(post.contentType, 'reel');
      expect(post.tags, ['flutter', 'dart']);
      expect(post.likeCount, 42);
      expect(post.isLiked, true);
      expect(post.isReel, true);
      expect(post.isVideo, false);
      expect(post.durationSeconds, 30);
    });

    test('handles minimal JSON with defaults', () {
      final json = <String, dynamic>{};
      final post = Post.fromJson(json);
      expect(post.id, '');
      expect(post.authorId, '');
      expect(post.content, '');
      expect(post.contentType, 'post');
      expect(post.tags, isEmpty);
      expect(post.likeCount, 0);
      expect(post.isLiked, false);
    });

    test('parses nested counts object', () {
      final json = {
        'id': 'post-1',
        'author_id': 'user-1',
        'content': 'Test',
        'created_at': '2026-03-16T10:00:00Z',
        'counts': {
          'likes': 100,
          'comments': 25,
          'shares': 10,
        },
      };

      final post = Post.fromJson(json);
      expect(post.likeCount, 100);
      expect(post.commentCount, 25);
      expect(post.shareCount, 10);
    });

    test('prefers top-level counts over nested counts', () {
      final json = {
        'id': 'post-1',
        'author_id': 'user-1',
        'content': 'Test',
        'created_at': '2026-03-16T10:00:00Z',
        'like_count': 200,
        'counts': {'likes': 100},
      };

      final post = Post.fromJson(json);
      expect(post.likeCount, 200);
    });

    test('type getters work correctly', () {
      expect(Post.fromJson({'content_type': 'reel'}).isReel, true);
      expect(Post.fromJson({'content_type': 'video'}).isVideo, true);
      expect(Post.fromJson({'content_type': 'poll'}).isPoll, true);
      expect(Post.fromJson({'content_type': 'post'}).isReel, false);
    });

    test('fallback key post_id is used when id is missing', () {
      final json = {'post_id': 'alt-id', 'content': 'test'};
      final post = Post.fromJson(json);
      expect(post.id, 'alt-id');
    });
  });

  group('Comment.fromJson', () {
    test('parses comment fields', () {
      final json = {
        'id': 'comment-1',
        'post_id': 'post-1',
        'user_id': 'user-1',
        'user_display_name': 'Jane',
        'text': 'Great post!',
        'like_count': 5,
        'created_at': '2026-03-16T10:00:00Z',
      };

      final comment = Comment.fromJson(json);
      expect(comment.id, 'comment-1');
      expect(comment.authorName, 'Jane');
      expect(comment.text, 'Great post!');
      expect(comment.likeCount, 5);
    });
  });
}
