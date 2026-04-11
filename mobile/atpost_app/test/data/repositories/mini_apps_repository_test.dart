import 'package:atpost_app/data/repositories/mini_apps_repository.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import '../../helpers/mocks.dart';
import '../../helpers/test_utils.dart';

void main() {
  late MockApiClient mockApi;
  late MiniAppsRepository repo;

  setUp(() {
    mockApi = MockApiClient();
    repo = MiniAppsRepository(mockApi);
  });

  group('listApps', () {
    test('parses nested items envelope and passes query params', () async {
      when(
        () => mockApi.get(
          '/v1/apps',
          queryParameters: any(named: 'queryParameters'),
        ),
      ).thenAnswer(
        (_) async => mockResponse(
          data: {
            'data': {
              'items': [
                {
                  'id': 'app-1',
                  'developer_id': 'dev-1',
                  'name': 'Weather',
                  'description': 'Forecasts',
                  'manifest_url':
                      'https://apps.example.com/weather/manifest.json',
                  'permissions': ['clipboard.write'],
                  'status': 'live',
                  'category': 'tools',
                  'install_count': 12,
                  'created_at': '2026-04-08T00:00:00Z',
                },
              ],
            },
          },
          requestPath: '/v1/apps',
        ),
      );

      final apps = await repo.listApps(
        category: 'tools',
        limit: 10,
        offset: 20,
      );

      expect(apps.length, 1);
      expect(apps.first.id, 'app-1');
      expect(apps.first.category, 'tools');

      final captured =
          verify(
                () => mockApi.get(
                  '/v1/apps',
                  queryParameters: captureAny(named: 'queryParameters'),
                ),
              ).captured.single
              as Map<String, dynamic>;
      expect(captured['category'], 'tools');
      expect(captured['limit'], 10);
      expect(captured['offset'], 20);
    });
  });

  group('getInstalledApps', () {
    test('parses granted permissions from array envelope', () async {
      when(() => mockApi.get('/v1/apps/installed')).thenAnswer(
        (_) async => mockResponse(
          data: {
            'data': [
              {
                'id': 'app-1',
                'developer_id': 'dev-1',
                'name': 'Weather',
                'description': 'Forecasts',
                'manifest_url':
                    'https://apps.example.com/weather/manifest.json',
                'permissions': ['clipboard.write', 'user.profile.read'],
                'granted_permissions': ['user.profile.read'],
                'status': 'live',
                'category': 'tools',
                'install_count': 12,
                'created_at': '2026-04-08T00:00:00Z',
              },
            ],
          },
          requestPath: '/v1/apps/installed',
        ),
      );

      final apps = await repo.getInstalledApps();

      expect(apps.length, 1);
      expect(apps.first.grantedPermissions, ['user.profile.read']);
    });
  });

  group('getAppWithInstallationState', () {
    test('merges granted permissions from installed apps', () async {
      when(() => mockApi.get('/v1/apps/app-1')).thenAnswer(
        (_) async => mockResponse(
          data: {
            'data': {
              'id': 'app-1',
              'developer_id': 'dev-1',
              'name': 'Weather',
              'description': 'Forecasts',
              'manifest_url': 'https://apps.example.com/weather/manifest.json',
              'permissions': ['clipboard.write', 'user.profile.read'],
              'status': 'live',
              'category': 'tools',
              'install_count': 12,
              'created_at': '2026-04-08T00:00:00Z',
            },
          },
          requestPath: '/v1/apps/app-1',
        ),
      );
      when(() => mockApi.get('/v1/apps/installed')).thenAnswer(
        (_) async => mockResponse(
          data: {
            'data': [
              {
                'id': 'app-1',
                'developer_id': 'dev-1',
                'name': 'Weather',
                'description': 'Forecasts',
                'manifest_url':
                    'https://apps.example.com/weather/manifest.json',
                'permissions': ['clipboard.write', 'user.profile.read'],
                'granted_permissions': ['user.profile.read'],
                'status': 'live',
                'category': 'tools',
                'install_count': 12,
                'created_at': '2026-04-08T00:00:00Z',
              },
            ],
          },
          requestPath: '/v1/apps/installed',
        ),
      );

      final app = await repo.getAppWithInstallationState('app-1');

      expect(app.isInstalled, isTrue);
      expect(app.grantedPermissions, ['user.profile.read']);
    });

    test('falls back to catalog app when installed app lookup fails', () async {
      when(() => mockApi.get('/v1/apps/app-1')).thenAnswer(
        (_) async => mockResponse(
          data: {
            'data': {
              'id': 'app-1',
              'developer_id': 'dev-1',
              'name': 'Weather',
              'description': 'Forecasts',
              'manifest_url': 'https://apps.example.com/weather/manifest.json',
              'permissions': ['clipboard.write'],
              'status': 'live',
              'category': 'tools',
              'install_count': 12,
              'created_at': '2026-04-08T00:00:00Z',
            },
          },
          requestPath: '/v1/apps/app-1',
        ),
      );
      when(
        () => mockApi.get('/v1/apps/installed'),
      ).thenThrow(Exception('boom'));

      final app = await repo.getAppWithInstallationState('app-1');

      expect(app.id, 'app-1');
      expect(app.isInstalled, isFalse);
      expect(app.grantedPermissions, isEmpty);
    });
  });

  group('installApp', () {
    test('posts granted permissions and unwraps response data', () async {
      when(
        () => mockApi.post('/v1/apps/app-1/install', data: any(named: 'data')),
      ).thenAnswer(
        (_) async => mockResponse(
          data: {
            'data': {
              'app_id': 'app-1',
              'granted_permissions': ['clipboard.write'],
            },
          },
          requestPath: '/v1/apps/app-1/install',
        ),
      );

      final result = await repo.installApp(
        'app-1',
        grantedPermissions: const ['clipboard.write'],
      );

      expect(result['app_id'], 'app-1');
      expect(result['granted_permissions'], ['clipboard.write']);
      verify(
        () => mockApi.post(
          '/v1/apps/app-1/install',
          data: {
            'granted_permissions': ['clipboard.write'],
          },
        ),
      ).called(1);
    });
  });

  group('getAppSession', () {
    test('unwraps runtime session envelope', () async {
      when(() => mockApi.get('/v1/apps/app-1/session')).thenAnswer(
        (_) async => mockResponse(
          data: {
            'data': {
              'app_id': 'app-1',
              'user_id': 'user-1',
              'token_type': 'Bearer',
              'access_token': 'token',
              'expires_in': 300,
              'granted_permissions': ['user.profile.read'],
            },
          },
          requestPath: '/v1/apps/app-1/session',
        ),
      );

      final session = await repo.getAppSession('app-1');

      expect(session['access_token'], 'token');
      expect(session['granted_permissions'], ['user.profile.read']);
    });
  });
}
