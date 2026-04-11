import 'package:atpost_app/features/mini_apps/mini_app_permissions.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('normalizeMiniAppPermissions', () {
    test('trims, deduplicates, and preserves first-seen order', () {
      final normalized = normalizeMiniAppPermissions([
        ' clipboard.write ',
        'user.profile.read',
        'clipboard.write',
        '',
      ]);

      expect(normalized, ['clipboard.write', 'user.profile.read']);
    });
  });

  group('miniAppPermissionFor', () {
    test('returns fallback metadata for unknown permissions', () {
      final definition = miniAppPermissionFor('custom.scope');
      expect(definition.key, 'custom.scope');
      expect(definition.title, 'custom.scope');
    });
  });
}
