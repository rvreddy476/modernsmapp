import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final bookmarksProvider = FutureProvider.autoDispose<List<Post>>((ref) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get('/v1/posts/bookmarks');
  final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
  return items.map((e) => Post.fromJson(e as Map<String, dynamic>)).toList();
});
