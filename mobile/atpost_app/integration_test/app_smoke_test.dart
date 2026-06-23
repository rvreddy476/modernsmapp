// Thin mobile E2E smoke (docs/TESTING.md W5). Boots the real app on a device/
// emulator and asserts it reaches its first interactive frame without crashing
// — this catches startup, DI (Riverpod), and router failures that unit/widget
// tests miss. Run:
//
//   flutter test integration_test/app_smoke_test.dart            (needs a device)
//
// The full login → post → feed journey needs seeded creds + a reachable
// backend; pass them via --dart-define and extend this file when the staging
// stack is wired into the nightly job.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import 'package:atpost_app/main.dart' as app;

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('app boots to first frame without crashing', (tester) async {
    app.main();
    // Let async startup (providers, router, session check) settle. Bounded so a
    // hung network call surfaces as a timeout rather than hanging forever.
    await tester.pumpAndSettle(const Duration(seconds: 10));

    // The app shell rendered.
    expect(find.byType(MaterialApp), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
