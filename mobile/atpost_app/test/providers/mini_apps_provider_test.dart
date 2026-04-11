import 'package:atpost_app/data/models/mini_app.dart';
import 'package:atpost_app/data/repositories/mini_apps_repository.dart';
import 'package:atpost_app/providers/mini_apps_provider.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/mocks.dart';

class _FakeMiniAppsRepository extends MiniAppsRepository {
  _FakeMiniAppsRepository({
    required List<MiniApp> allApps,
    List<MiniApp> installedApps = const [],
    this.failInstallForId,
    this.failUninstallForId,
  }) : _allApps = List<MiniApp>.from(allApps),
       _installedApps = List<MiniApp>.from(installedApps),
       super(MockApiClient());

  final List<MiniApp> _allApps;
  final List<MiniApp> _installedApps;
  final String? failInstallForId;
  final String? failUninstallForId;

  String? lastListedCategory;
  int installCalls = 0;
  int uninstallCalls = 0;

  @override
  Future<List<MiniApp>> listApps({
    String? category,
    int limit = 20,
    int offset = 0,
  }) async {
    lastListedCategory = category;
    final installedById = {for (final app in _installedApps) app.id: app};
    return _allApps
        .where((app) => category == null || app.category == category)
        .map((app) => app.withInstalledStateFrom(installedById[app.id]))
        .toList();
  }

  @override
  Future<List<MiniApp>> getInstalledApps() async {
    return _installedApps
        .map((app) => app.copyWith(isInstalled: true))
        .toList(growable: false);
  }

  @override
  Future<Map<String, dynamic>> installApp(
    String appId, {
    List<String> grantedPermissions = const [],
  }) async {
    installCalls += 1;
    if (appId == failInstallForId) {
      throw Exception('install failed');
    }

    final index = _allApps.indexWhere((app) => app.id == appId);
    if (index == -1) {
      return const {};
    }

    final current = _allApps[index];
    final updated = current.copyWith(
      isInstalled: true,
      installCount: current.installCount + 1,
      grantedPermissions: grantedPermissions,
    );
    _allApps[index] = updated;
    _installedApps.removeWhere((app) => app.id == appId);
    _installedApps.add(updated);
    return {'id': appId};
  }

  @override
  Future<void> uninstallApp(String appId) async {
    uninstallCalls += 1;
    if (appId == failUninstallForId) {
      throw Exception('uninstall failed');
    }

    final index = _allApps.indexWhere((app) => app.id == appId);
    if (index != -1) {
      _allApps[index] = _allApps[index].copyWith(
        isInstalled: false,
        grantedPermissions: const [],
      );
    }
    _installedApps.removeWhere((app) => app.id == appId);
  }
}

MiniApp _buildApp({
  required String id,
  required String name,
  required String category,
  bool isInstalled = false,
  List<String> grantedPermissions = const [],
}) {
  return MiniApp(
    id: id,
    developerId: 'dev-$id',
    name: name,
    description: '$name description',
    manifestUrl: 'https://apps.example.com/$id/manifest.json',
    permissions: const ['user.profile.read', 'clipboard.write'],
    status: 'live',
    category: category,
    installCount: 10,
    createdAt: DateTime.parse('2026-04-08T00:00:00Z'),
    isInstalled: isInstalled,
    grantedPermissions: grantedPermissions,
  );
}

Future<void> _settleNotifier() async {
  await Future<void>.delayed(Duration.zero);
  await Future<void>.delayed(Duration.zero);
}

MiniAppsState _stateValue(MiniAppsNotifier notifier) {
  final value = notifier.state.value;
  expect(value, isNotNull);
  return value!;
}

void main() {
  group('MiniAppsNotifier', () {
    test('refresh syncs installed state into the catalog list', () async {
      final installedApp = _buildApp(
        id: 'weather',
        name: 'Weather',
        category: 'tools',
        isInstalled: true,
        grantedPermissions: const ['user.profile.read'],
      );
      final repo = _FakeMiniAppsRepository(
        allApps: [
          _buildApp(id: 'weather', name: 'Weather', category: 'tools'),
          _buildApp(id: 'quiz', name: 'Quiz', category: 'games'),
        ],
        installedApps: [installedApp],
      );

      final notifier = MiniAppsNotifier(repo);
      await _settleNotifier();

      final state = _stateValue(notifier);
      final weather = state.allApps.firstWhere((app) => app.id == 'weather');

      expect(weather.isInstalled, isTrue);
      expect(weather.grantedPermissions, const ['user.profile.read']);
      expect(state.installedApps.map((app) => app.id), contains('weather'));
    });

    test(
      'setCategory clears the selected category when switching back to all',
      () async {
        final repo = _FakeMiniAppsRepository(
          allApps: [
            _buildApp(id: 'weather', name: 'Weather', category: 'tools'),
            _buildApp(id: 'quiz', name: 'Quiz', category: 'games'),
          ],
        );

        final notifier = MiniAppsNotifier(repo);
        await _settleNotifier();

        await notifier.setCategory('games');
        expect(_stateValue(notifier).selectedCategory, 'games');
        expect(_stateValue(notifier).allApps.map((app) => app.id), ['quiz']);

        await notifier.setCategory(null);

        final state = _stateValue(notifier);
        expect(repo.lastListedCategory, isNull);
        expect(state.selectedCategory, isNull);
        expect(state.allApps.map((app) => app.id), ['weather', 'quiz']);
      },
    );

    test(
      'installApp adds the app to installed state and keeps permissions',
      () async {
        final repo = _FakeMiniAppsRepository(
          allApps: [
            _buildApp(id: 'weather', name: 'Weather', category: 'tools'),
            _buildApp(id: 'quiz', name: 'Quiz', category: 'games'),
          ],
        );

        final notifier = MiniAppsNotifier(repo);
        await _settleNotifier();

        await notifier.installApp(
          'quiz',
          grantedPermissions: const ['clipboard.write'],
        );

        final state = _stateValue(notifier);
        final quiz = state.allApps.firstWhere((app) => app.id == 'quiz');

        expect(repo.installCalls, 1);
        expect(quiz.isInstalled, isTrue);
        expect(quiz.grantedPermissions, const ['clipboard.write']);
        expect(state.installedApps.map((app) => app.id), contains('quiz'));
      },
    );

    test(
      'installApp rolls back optimistic state when the repository fails',
      () async {
        final repo = _FakeMiniAppsRepository(
          allApps: [
            _buildApp(id: 'weather', name: 'Weather', category: 'tools'),
            _buildApp(id: 'quiz', name: 'Quiz', category: 'games'),
          ],
          failInstallForId: 'quiz',
        );

        final notifier = MiniAppsNotifier(repo);
        await _settleNotifier();

        await notifier.installApp(
          'quiz',
          grantedPermissions: const ['clipboard.write'],
        );

        final state = _stateValue(notifier);
        final quiz = state.allApps.firstWhere((app) => app.id == 'quiz');

        expect(repo.installCalls, 1);
        expect(quiz.isInstalled, isFalse);
        expect(quiz.grantedPermissions, isEmpty);
        expect(state.installedApps, isEmpty);
      },
    );

    test(
      'uninstallApp removes the app from installed state and clears permissions',
      () async {
        final installedQuiz = _buildApp(
          id: 'quiz',
          name: 'Quiz',
          category: 'games',
          isInstalled: true,
          grantedPermissions: const ['clipboard.write'],
        );
        final repo = _FakeMiniAppsRepository(
          allApps: [
            _buildApp(id: 'weather', name: 'Weather', category: 'tools'),
            installedQuiz,
          ],
          installedApps: [installedQuiz],
        );

        final notifier = MiniAppsNotifier(repo);
        await _settleNotifier();

        await notifier.uninstallApp('quiz');

        final state = _stateValue(notifier);
        final quiz = state.allApps.firstWhere((app) => app.id == 'quiz');

        expect(repo.uninstallCalls, 1);
        expect(quiz.isInstalled, isFalse);
        expect(quiz.grantedPermissions, isEmpty);
        expect(
          state.installedApps.map((app) => app.id),
          isNot(contains('quiz')),
        );
      },
    );

    test(
      'uninstallApp rolls back optimistic state when the repository fails',
      () async {
        final installedQuiz = _buildApp(
          id: 'quiz',
          name: 'Quiz',
          category: 'games',
          isInstalled: true,
          grantedPermissions: const ['clipboard.write'],
        );
        final repo = _FakeMiniAppsRepository(
          allApps: [
            _buildApp(id: 'weather', name: 'Weather', category: 'tools'),
            installedQuiz,
          ],
          installedApps: [installedQuiz],
          failUninstallForId: 'quiz',
        );

        final notifier = MiniAppsNotifier(repo);
        await _settleNotifier();

        await notifier.uninstallApp('quiz');

        final state = _stateValue(notifier);
        final quiz = state.allApps.firstWhere((app) => app.id == 'quiz');

        expect(repo.uninstallCalls, 1);
        expect(quiz.isInstalled, isTrue);
        expect(quiz.grantedPermissions, const ['clipboard.write']);
        expect(state.installedApps.map((app) => app.id), contains('quiz'));
      },
    );
  });
}
