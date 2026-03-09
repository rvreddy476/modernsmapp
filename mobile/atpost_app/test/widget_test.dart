import 'package:atpost_app/app/atpost_app.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('app bootstraps without crashing', (WidgetTester tester) async {
    await tester.pumpWidget(const ProviderScope(child: AtpostApp()));
    await tester.pump();

    expect(find.byType(MaterialApp), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
