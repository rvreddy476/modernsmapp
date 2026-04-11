import 'package:atpost_app/data/models/mini_app_manifest.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('MiniAppManifest.fromJson', () {
    test('resolves relative start_url and allowed origins', () {
      final manifest = MiniAppManifest.fromJson(
        {
          'start_url': '/launch',
          'allowed_origins': [
            'https://widgets.example.com',
            'https://cdn.example.com/assets/app.js',
          ],
          'bridge_version': '1',
        },
        manifestUri: Uri.parse(
          'https://developer.example.com/app/manifest.json',
        ),
      );

      expect(
        manifest.startUri,
        Uri.parse('https://developer.example.com/launch'),
      );
      expect(
        manifest.allowedOrigins,
        containsAll(<String>[
          'https://developer.example.com',
          'https://widgets.example.com',
          'https://cdn.example.com',
        ]),
      );
      expect(manifest.bridgeVersion, '1');
    });

    test('throws when start_url is missing', () {
      expect(
        () => MiniAppManifest.fromJson(
          const <String, dynamic>{},
          manifestUri: Uri.parse('https://developer.example.com/manifest.json'),
        ),
        throwsFormatException,
      );
    });
  });

  group('MiniAppLaunchConfig', () {
    test(
      'legacy config only allows the launch origin and internal schemes',
      () {
        final config = MiniAppLaunchConfig.legacy(
          entryUri: Uri.parse('https://apps.example.com/play'),
        );

        expect(
          config.allows(Uri.parse('https://apps.example.com/next')),
          isTrue,
        );
        expect(
          config.allows(Uri.parse('https://other.example.com/next')),
          isFalse,
        );
        expect(config.allows(Uri.parse('about:blank')), isTrue);
      },
    );
  });
}
