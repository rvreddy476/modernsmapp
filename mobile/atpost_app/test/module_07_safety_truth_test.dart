import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/shared/widgets/provenance_badge.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'altered-content disclosure survives parse, copy, and serialization',
    () {
      final post = Post.fromJson({
        'id': 'post-1',
        'author_id': 'author-1',
        'content': 'Synthetic media example',
        'created_at': '2026-08-12T12:00:00Z',
        'altered_content': true,
      });

      expect(post.alteredContent, isTrue);
      expect(post.copyWith(likeCount: 1).alteredContent, isTrue);
      expect(post.toJson()['altered_content'], isTrue);
    },
  );

  testWidgets('provenance badge exposes visible and semantic disclosure', (
    tester,
  ) async {
    await tester.pumpWidget(
      const MaterialApp(home: Scaffold(body: ProvenanceBadge())),
    );

    expect(find.text('AI / altered content'), findsOneWidget);
    expect(
      find.bySemanticsLabel(
        'Creator disclosed AI-generated or significantly altered content',
      ),
      findsOneWidget,
    );
  });

  test('crowd-reviewer surface is default closed in the client', () {
    expect(Environment.reviewerPublicEnabled, isFalse);
  });
}
