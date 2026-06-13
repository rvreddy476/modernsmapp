// In-video product tag providers. The player feature watches
// productTagsByPostProvider(postId) and renders the overlay; the
// composer feature uses createProductTag for tag placement.

import 'package:atpost_app/data/models/product_tag.dart';
import 'package:atpost_app/data/repositories/product_tags_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Active product tags for a post. Cached by Riverpod's
/// AsyncValue.autoDispose; the next player open re-fetches automatically.
final productTagsByPostProvider =
    FutureProvider.autoDispose.family<List<PostProductTag>, String>(
  (ref, postId) async {
    final repo = ref.watch(productTagsRepositoryProvider);
    return repo.listByPost(postId);
  },
);
