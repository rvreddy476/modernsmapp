import 'package:atpost_app/core/config/environment.dart';
import 'package:flutter_test/flutter_test.dart';

/// Cloudflare Access service token wiring.
///
/// The values come from `String.fromEnvironment`, which resolves at COMPILE
/// time, so a single run can only ever observe one of the two modes. These
/// tests therefore assert the invariant that must hold in BOTH, and branch on
/// which build they are running inside — otherwise adding --dart-define would
/// make the suite fail rather than prove anything.
///
/// Default mode (every existing build — localhost, CI, public release):
///
///   flutter test test/access_service_token_test.dart
///
/// Configured mode (a private build aimed at an Access-protected hostname):
///
///   flutter test --dart-define=CF_ACCESS_CLIENT_ID=abc.access \
///                --dart-define=CF_ACCESS_CLIENT_SECRET=shhh \
///                test/access_service_token_test.dart
void main() {
  group('Cloudflare Access service token', () {
    test('unconfigured builds send no Access headers at all', () {
      if (Environment.hasAccessServiceToken) {
        // Configured build: this guarantee does not apply. Assert the opposite
        // property instead, so the run still proves something real.
        expect(Environment.accessServiceTokenHeaders, isNotEmpty);
        return;
      }
      expect(Environment.accessServiceTokenHeaders, isEmpty);
    });

    test('configured builds send exactly the two header names Cloudflare '
        'expects', () {
      if (!Environment.hasAccessServiceToken) {
        // Nothing to check in a default build; the test above covers it.
        return;
      }
      // Cloudflare rejects any other spelling, and a typo surfaces as an HTML
      // login page rather than a clear error — worth pinning exactly.
      expect(Environment.accessServiceTokenHeaders.keys.toSet(), {
        'CF-Access-Client-Id',
        'CF-Access-Client-Secret',
      });
      expect(
        Environment.accessServiceTokenHeaders.values.every((v) => v.isNotEmpty),
        isTrue,
      );
    });

    test('presence flag is derived from the headers, never tracked separately',
        () {
      // hasAccessServiceToken is what gets logged. If it could disagree with
      // the headers actually sent, the log would lie in exactly the situation
      // someone is using it to debug.
      expect(
        Environment.hasAccessServiceToken,
        Environment.accessServiceTokenHeaders.isNotEmpty,
      );
    });
  });
}
