import 'package:atpost_app/data/models/mini_app.dart';
import 'package:atpost_app/data/repositories/mini_apps_repository.dart';
import 'package:atpost_app/features/mini_apps/mini_app_detail_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../helpers/mocks.dart';

class _FakeMiniAppsRepository extends MiniAppsRepository {
  _FakeMiniAppsRepository({
    required MiniApp app,
    this.failInstall = false,
    this.failUninstall = false,
  }) : _app = app,
       super(MockApiClient());

  MiniApp _app;
  final bool failInstall;
  final bool failUninstall;

  int installCalls = 0;
  int uninstallCalls = 0;

  @override
  Future<MiniApp> getAppWithInstallationState(String id) async {
    return _app;
  }

  @override
  Future<List<MiniApp>> listApps({
    String? category,
    int limit = 20,
    int offset = 0,
  }) async {
    if (category != null && _app.category != category) {
      return const [];
    }
    return [_app];
  }

  @override
  Future<List<MiniApp>> getInstalledApps() async {
    if (!_app.isInstalled) return const [];
    return [_app];
  }

  @override
  Future<Map<String, dynamic>> installApp(
    String appId, {
    List<String> grantedPermissions = const [],
  }) async {
    installCalls += 1;
    if (failInstall) {
      throw Exception('install failed');
    }

    _app = _app.copyWith(
      isInstalled: true,
      installCount: _app.installCount + 1,
      grantedPermissions: grantedPermissions,
    );
    return {'id': appId};
  }

  @override
  Future<void> uninstallApp(String appId) async {
    uninstallCalls += 1;
    if (failUninstall) {
      throw Exception('uninstall failed');
    }

    _app = _app.copyWith(isInstalled: false, grantedPermissions: const []);
  }
}

MiniApp _buildApp({bool isInstalled = false}) {
  return MiniApp(
    id: 'weather',
    developerId: 'dev-weather',
    name: 'Weather',
    description: 'Forecasts on demand',
    manifestUrl: 'https://apps.example.com/weather/manifest.json',
    permissions: const [],
    status: 'live',
    category: 'tools',
    installCount: 12,
    createdAt: DateTime.parse('2026-04-08T00:00:00Z'),
    isInstalled: isInstalled,
  );
}

Future<_FakeMiniAppsRepository> _pumpDetailScreen(
  WidgetTester tester, {
  required MiniApp app,
  bool failInstall = false,
  bool failUninstall = false,
}) async {
  final repo = _FakeMiniAppsRepository(
    app: app,
    failInstall: failInstall,
    failUninstall: failUninstall,
  );

  await tester.pumpWidget(
    ProviderScope(
      overrides: [miniAppsRepositoryProvider.overrideWithValue(repo)],
      child: const MaterialApp(home: MiniAppDetailScreen(appId: 'weather')),
    ),
  );
  await tester.pumpAndSettle();

  return repo;
}

void main() {
  group('MiniAppDetailScreen', () {
    testWidgets('installs the app and switches to open and uninstall actions', (
      tester,
    ) async {
      final repo = await _pumpDetailScreen(tester, app: _buildApp());

      expect(find.text('Install'), findsOneWidget);

      await tester.tap(find.text('Install'));
      await tester.pumpAndSettle();

      expect(repo.installCalls, 1);
      expect(find.text('Open'), findsOneWidget);
      expect(find.text('Uninstall'), findsOneWidget);
      expect(find.text('App installed!'), findsOneWidget);
    });

    testWidgets('shows an error snackbar when install fails', (tester) async {
      final repo = await _pumpDetailScreen(
        tester,
        app: _buildApp(),
        failInstall: true,
      );

      await tester.tap(find.text('Install'));
      await tester.pumpAndSettle();

      expect(repo.installCalls, 1);
      expect(find.text('Install'), findsOneWidget);
      expect(find.text('Install failed'), findsOneWidget);
      expect(find.text('Open'), findsNothing);
    });

    testWidgets('uninstalls the app and restores the install action', (
      tester,
    ) async {
      final repo = await _pumpDetailScreen(
        tester,
        app: _buildApp(isInstalled: true),
      );

      expect(find.text('Open'), findsOneWidget);
      expect(find.text('Uninstall'), findsOneWidget);

      await tester.tap(find.text('Uninstall'));
      await tester.pumpAndSettle();

      expect(repo.uninstallCalls, 1);
      expect(find.text('Install'), findsOneWidget);
      expect(find.text('App uninstalled'), findsOneWidget);
      expect(find.text('Open'), findsNothing);
    });
  });
}
