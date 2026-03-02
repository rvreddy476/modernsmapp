import 'package:atpost_app/app/atpost_app.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('app shell renders home header', (WidgetTester tester) async {
    await tester.pumpWidget(const ProviderScope(child: AtpostApp()));
    await tester.pump(const Duration(milliseconds: 300));

    expect(find.text('atpost'), findsOneWidget);
  });
}
