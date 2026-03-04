import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final commentsProvider =
    FutureProvider.autoDispose.family<List<Comment>, String>((ref, postId) async {
  return ref.watch(postRepositoryProvider).getComments(postId);
});
