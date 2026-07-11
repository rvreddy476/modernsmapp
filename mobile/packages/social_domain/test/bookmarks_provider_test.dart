// bookmarksProvider parses GET /v1/posts/bookmarks (post-service).
import 'package:atpost_network/api_client.dart';
import 'package:social_domain/providers/bookmarks_provider.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import 'helpers.dart';

void main() {
  late MockApiClient api;
  late ProviderContainer container;

  setUp(() {
    api = MockApiClient();
    container = ProviderContainer(
      overrides: [apiClientProvider.overrideWithValue(api)],
    );
    addTearDown(container.dispose);
  });

  test('parses the items envelope into posts', () async {
    when(() => api.get('/v1/posts/bookmarks')).thenAnswer(
      (_) async => ok({
        'data': {
          'items': [
            {
              'id': 'p1',
              'author_id': 'alice',
              'content': 'saved post',
              'created_at': '2026-07-01T10:00:00Z',
            },
          ],
        },
      }),
    );

    final sub = container.listen(bookmarksProvider, (_, _) {});
    addTearDown(sub.close);
    final posts = await container.read(bookmarksProvider.future);

    expect(posts, hasLength(1));
    expect(posts.single.id, 'p1');
    expect(posts.single.content, 'saved post');
  });

  test('missing items resolves to an empty list, not an error', () async {
    when(() => api.get('/v1/posts/bookmarks'))
        .thenAnswer((_) async => ok({'data': <String, dynamic>{}}));

    final sub = container.listen(bookmarksProvider, (_, _) {});
    addTearDown(sub.close);

    expect(await container.read(bookmarksProvider.future), isEmpty);
  });
}
