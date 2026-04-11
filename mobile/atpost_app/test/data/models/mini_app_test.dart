import 'package:atpost_app/data/models/mini_app.dart';
import 'package:flutter_test/flutter_test.dart';

MiniApp _buildApp({
  bool isInstalled = false,
  List<String> grantedPermissions = const [],
}) {
  return MiniApp(
    id: 'app-1',
    developerId: 'dev-1',
    name: 'Weather',
    description: 'Forecasts',
    manifestUrl: 'https://apps.example.com/manifest.json',
    permissions: const ['clipboard.write', 'user.profile.read'],
    status: 'live',
    category: 'tools',
    installCount: 42,
    createdAt: DateTime.parse('2026-01-01T00:00:00Z'),
    isInstalled: isInstalled,
    grantedPermissions: grantedPermissions,
  );
}

void main() {
  group('MiniApp.withInstalledStateFrom', () {
    test(
      'copies install status and granted permissions from installed app',
      () {
        final app = _buildApp();
        final installedApp = _buildApp(
          isInstalled: true,
          grantedPermissions: const ['user.profile.read'],
        );

        final merged = app.withInstalledStateFrom(installedApp);

        expect(merged.isInstalled, isTrue);
        expect(merged.grantedPermissions, const ['user.profile.read']);
        expect(merged.installCount, 42);
      },
    );

    test('clears install-only state when installed app is missing', () {
      final app = _buildApp(
        isInstalled: true,
        grantedPermissions: const ['clipboard.write'],
      );

      final merged = app.withInstalledStateFrom(null);

      expect(merged.isInstalled, isFalse);
      expect(merged.grantedPermissions, isEmpty);
    });
  });
}
