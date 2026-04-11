import 'dart:convert';

import 'package:atpost_app/data/models/mini_app.dart';
import 'package:atpost_app/data/models/mini_app_manifest.dart';
import 'package:atpost_app/data/repositories/mini_apps_repository.dart';
import 'package:atpost_app/features/mini_apps/mini_app_sandbox_screen.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:mocktail/mocktail.dart';
import 'package:webview_flutter_platform_interface/webview_flutter_platform_interface.dart';

import '../../helpers/mocks.dart';

class _FakeMiniAppsRepository extends MiniAppsRepository {
  _FakeMiniAppsRepository(this.app, {DateTime? sessionExpiresAt})
    : sessionExpiresAt = sessionExpiresAt?.toUtc(),
      super(MockApiClient());

  final MiniApp app;
  final DateTime? sessionExpiresAt;
  int sessionRequests = 0;

  @override
  Future<List<MiniApp>> listApps({
    String? category,
    int limit = 20,
    int offset = 0,
  }) async {
    return [app];
  }

  @override
  Future<List<MiniApp>> getInstalledApps() async {
    return [app];
  }

  @override
  Future<MiniApp> getAppWithInstallationState(String id) async {
    return app;
  }

  @override
  Future<Map<String, dynamic>> getAppSession(String appId) async {
    sessionRequests += 1;
    final expiresAt =
        sessionExpiresAt ??
        DateTime.now().toUtc().add(const Duration(minutes: 5));
    return {
      'app_id': appId,
      'user_id': 'user-1',
      'token_type': 'Bearer',
      'access_token': 'session-token',
      'expires_at': expiresAt.toIso8601String(),
      'expires_in': 300,
      'issuer': 'atpost-mini-app-runtime',
      'audience': 'mini-app:$appId',
      'granted_permissions': app.grantedPermissions,
    };
  }

  @override
  Future<MiniAppLaunchConfig> resolveLaunchConfig(MiniApp app) async {
    const manifestUri = 'https://apps.example.com/weather/manifest.json';
    final manifest = MiniAppManifest.fromJson(const {
      'start_url': '/launch',
      'allowed_origins': ['https://apps.example.com'],
      'bridge_version': '1',
    }, manifestUri: Uri.parse(manifestUri));
    return MiniAppLaunchConfig.fromManifest(
      manifestUri: Uri.parse(manifestUri),
      manifest: manifest,
    );
  }
}

class _FakeWebViewPlatform extends WebViewPlatform {
  _FakePlatformWebViewController? lastCreatedController;

  @override
  PlatformWebViewController createPlatformWebViewController(
    PlatformWebViewControllerCreationParams params,
  ) {
    final controller = _FakePlatformWebViewController(params);
    lastCreatedController = controller;
    return controller;
  }

  @override
  PlatformNavigationDelegate createPlatformNavigationDelegate(
    PlatformNavigationDelegateCreationParams params,
  ) {
    return _FakePlatformNavigationDelegate(params);
  }

  @override
  PlatformWebViewWidget createPlatformWebViewWidget(
    PlatformWebViewWidgetCreationParams params,
  ) {
    return _FakePlatformWebViewWidget(params);
  }

  @override
  PlatformWebViewCookieManager createPlatformCookieManager(
    PlatformWebViewCookieManagerCreationParams params,
  ) {
    return _FakePlatformWebViewCookieManager(params);
  }
}

class _FakePlatformWebViewController extends PlatformWebViewController {
  _FakePlatformWebViewController(super.params) : super.implementation();

  final Map<String, void Function(JavaScriptMessage)> _channels =
      <String, void Function(JavaScriptMessage)>{};
  final List<String> executedJavaScript = <String>[];
  final List<Uri> blockedNavigationUris = <Uri>[];
  Uri? lastLoadedUri;
  _FakePlatformNavigationDelegate? _navigationDelegate;

  @override
  Future<void> setJavaScriptMode(JavaScriptMode javaScriptMode) async {}

  @override
  Future<void> setBackgroundColor(Color color) async {}

  @override
  Future<void> addJavaScriptChannel(
    JavaScriptChannelParams javaScriptChannelParams,
  ) async {
    _channels[javaScriptChannelParams.name] =
        javaScriptChannelParams.onMessageReceived;
  }

  @override
  Future<void> setPlatformNavigationDelegate(
    PlatformNavigationDelegate handler,
  ) async {
    _navigationDelegate = handler as _FakePlatformNavigationDelegate;
  }

  @override
  Future<void> loadRequest(LoadRequestParams params) async {
    final decision =
        await (_navigationDelegate?.onNavigationRequest?.call(
              NavigationRequest(url: params.uri.toString(), isMainFrame: true),
            ) ??
            NavigationDecision.navigate);

    if (decision == NavigationDecision.prevent) {
      blockedNavigationUris.add(params.uri);
      return;
    }

    lastLoadedUri = params.uri;
    _navigationDelegate?.onPageStarted?.call(params.uri.toString());
    _navigationDelegate?.onProgress?.call(100);
    _navigationDelegate?.onPageFinished?.call(params.uri.toString());
  }

  @override
  Future<void> runJavaScript(String javaScript) async {
    executedJavaScript.add(javaScript);
  }

  void dispatchJavaScriptMessage(String channelName, String message) {
    final callback = _channels[channelName];
    if (callback == null) {
      throw StateError('JavaScript channel $channelName was not registered');
    }
    callback(JavaScriptMessage(message: message));
  }

  Future<void> simulateNavigation(String url) async {
    await loadRequest(LoadRequestParams(uri: Uri.parse(url)));
  }
}

class _FakePlatformNavigationDelegate extends PlatformNavigationDelegate {
  _FakePlatformNavigationDelegate(super.params) : super.implementation();

  NavigationRequestCallback? onNavigationRequest;
  PageEventCallback? onPageStarted;
  PageEventCallback? onPageFinished;
  ProgressCallback? onProgress;
  WebResourceErrorCallback? onWebResourceError;
  UrlChangeCallback? onUrlChange;
  HttpAuthRequestCallback? onHttpAuthRequest;
  HttpResponseErrorCallback? onHttpError;
  SslAuthErrorCallback? onSslAuthError;

  @override
  Future<void> setOnNavigationRequest(
    NavigationRequestCallback onNavigationRequest,
  ) async {
    this.onNavigationRequest = onNavigationRequest;
  }

  @override
  Future<void> setOnPageStarted(PageEventCallback onPageStarted) async {
    this.onPageStarted = onPageStarted;
  }

  @override
  Future<void> setOnPageFinished(PageEventCallback onPageFinished) async {
    this.onPageFinished = onPageFinished;
  }

  @override
  Future<void> setOnProgress(ProgressCallback onProgress) async {
    this.onProgress = onProgress;
  }

  @override
  Future<void> setOnWebResourceError(
    WebResourceErrorCallback onWebResourceError,
  ) async {
    this.onWebResourceError = onWebResourceError;
  }

  @override
  Future<void> setOnUrlChange(UrlChangeCallback onUrlChange) async {
    this.onUrlChange = onUrlChange;
  }

  @override
  Future<void> setOnHttpAuthRequest(
    HttpAuthRequestCallback onHttpAuthRequest,
  ) async {
    this.onHttpAuthRequest = onHttpAuthRequest;
  }

  @override
  Future<void> setOnHttpError(HttpResponseErrorCallback onHttpError) async {
    this.onHttpError = onHttpError;
  }

  @override
  Future<void> setOnSSlAuthError(SslAuthErrorCallback onSslAuthError) async {
    this.onSslAuthError = onSslAuthError;
  }
}

class _FakePlatformWebViewWidget extends PlatformWebViewWidget {
  _FakePlatformWebViewWidget(super.params) : super.implementation();

  @override
  Widget build(BuildContext context) {
    return const ColoredBox(
      key: ValueKey<String>('fake-webview'),
      color: Colors.black,
    );
  }
}

class _FakePlatformWebViewCookieManager extends PlatformWebViewCookieManager {
  _FakePlatformWebViewCookieManager(super.params) : super.implementation();
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  final originalPlatform = WebViewPlatform.instance;

  setUp(() {
    WebViewPlatform.instance = _FakeWebViewPlatform();
  });

  tearDown(() {
    if (originalPlatform != null) {
      WebViewPlatform.instance = originalPlatform;
    }
  });

  MiniApp buildInstalledMiniApp({
    List<String> grantedPermissions = const ['user.profile.read'],
  }) {
    return MiniApp(
      id: 'app-1',
      developerId: 'dev-1',
      name: 'Weather',
      description: 'Forecasts',
      manifestUrl: 'https://apps.example.com/weather/manifest.json',
      permissions: const ['user.profile.read'],
      status: 'live',
      category: 'tools',
      installCount: 12,
      createdAt: DateTime.parse('2026-04-08T00:00:00Z'),
      isInstalled: true,
      grantedPermissions: grantedPermissions,
    );
  }

  Future<
    (_FakeMiniAppsRepository, _FakePlatformWebViewController, MockAuthService)
  >
  pumpSandbox(
    WidgetTester tester, {
    required bool isAuthenticated,
    List<String> grantedPermissions = const ['user.profile.read'],
    DateTime? sessionExpiresAt,
  }) async {
    final app = buildInstalledMiniApp(grantedPermissions: grantedPermissions);
    final fakeRepo = _FakeMiniAppsRepository(
      app,
      sessionExpiresAt: sessionExpiresAt,
    );
    final mockAuth = MockAuthService();
    when(() => mockAuth.isAuthenticated).thenReturn(isAuthenticated);
    when(() => mockAuth.userId).thenReturn(isAuthenticated ? 'user-1' : null);

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          miniAppsRepositoryProvider.overrideWithValue(fakeRepo),
          authServiceProvider.overrideWithValue(mockAuth),
        ],
        child: const MaterialApp(home: MiniAppSandboxScreen(appId: 'app-1')),
      ),
    );
    await tester.pumpAndSettle();

    final fakePlatform = WebViewPlatform.instance! as _FakeWebViewPlatform;
    final controller = fakePlatform.lastCreatedController;
    expect(controller, isNotNull);

    return (fakeRepo, controller!, mockAuth);
  }

  testWidgets(
    'loads a manifest-backed mini app and completes a session bridge request',
    (tester) async {
      final (fakeRepo, controller, _) = await pumpSandbox(
        tester,
        isAuthenticated: true,
      );

      expect(find.text('Weather'), findsOneWidget);
      expect(
        find.byKey(const ValueKey<String>('fake-webview')),
        findsOneWidget,
      );
      expect(
        controller.lastLoadedUri,
        Uri.parse('https://apps.example.com/launch'),
      );
      expect(
        controller.executedJavaScript.any(
          (code) => code.contains('window.__PostbookBridgeRuntime'),
        ),
        isTrue,
      );

      controller.dispatchJavaScriptMessage(
        'PostbookBridge',
        jsonEncode({
          'request_id': 'req-1',
          'type': 'session.get',
          'payload': <String, dynamic>{},
        }),
      );
      await tester.pumpAndSettle();

      expect(fakeRepo.sessionRequests, 1);
      expect(
        controller.executedJavaScript.any(
          (code) => code.contains('session-token') && code.contains('req-1'),
        ),
        isTrue,
      );
    },
  );

  testWidgets('reuses the cached session until it is near expiry', (
    tester,
  ) async {
    final (fakeRepo, controller, _) = await pumpSandbox(
      tester,
      isAuthenticated: true,
    );

    controller.dispatchJavaScriptMessage(
      'PostbookBridge',
      jsonEncode({
        'request_id': 'req-cache-1',
        'type': 'session.get',
        'payload': <String, dynamic>{},
      }),
    );
    await tester.pumpAndSettle();

    controller.dispatchJavaScriptMessage(
      'PostbookBridge',
      jsonEncode({
        'request_id': 'req-cache-2',
        'type': 'session.get',
        'payload': <String, dynamic>{},
      }),
    );
    await tester.pumpAndSettle();

    expect(fakeRepo.sessionRequests, 1);
    expect(
      controller.executedJavaScript
          .where((code) => code.contains('session-token'))
          .length,
      greaterThanOrEqualTo(2),
    );
  });

  testWidgets('refreshes the session when the cached token is near expiry', (
    tester,
  ) async {
    final (fakeRepo, controller, _) = await pumpSandbox(
      tester,
      isAuthenticated: true,
      sessionExpiresAt: DateTime.now().toUtc().add(const Duration(seconds: 10)),
    );

    controller.dispatchJavaScriptMessage(
      'PostbookBridge',
      jsonEncode({
        'request_id': 'req-refresh-1',
        'type': 'session.get',
        'payload': <String, dynamic>{},
      }),
    );
    await tester.pumpAndSettle();

    controller.dispatchJavaScriptMessage(
      'PostbookBridge',
      jsonEncode({
        'request_id': 'req-refresh-2',
        'type': 'session.get',
        'payload': <String, dynamic>{},
      }),
    );
    await tester.pumpAndSettle();

    expect(fakeRepo.sessionRequests, 2);
  });

  testWidgets('blocks navigation outside allowed origins', (tester) async {
    final (_, controller, _) = await pumpSandbox(tester, isAuthenticated: true);

    await controller.simulateNavigation('https://evil.example.com/phishing');
    await tester.pumpAndSettle();

    expect(
      controller.lastLoadedUri,
      Uri.parse('https://apps.example.com/launch'),
    );
    expect(
      controller.blockedNavigationUris,
      contains(Uri.parse('https://evil.example.com/phishing')),
    );
  });

  testWidgets('returns a bridge error when clipboard access was not granted', (
    tester,
  ) async {
    final (fakeRepo, controller, _) = await pumpSandbox(
      tester,
      isAuthenticated: true,
      grantedPermissions: const [],
    );

    controller.dispatchJavaScriptMessage(
      'PostbookBridge',
      jsonEncode({
        'request_id': 'req-clipboard-denied',
        'type': 'clipboard.write',
        'payload': <String, dynamic>{'text': 'copy me'},
      }),
    );
    await tester.pumpAndSettle();

    expect(fakeRepo.sessionRequests, 0);
    expect(
      controller.executedJavaScript.any(
        (code) =>
            code.contains('req-clipboard-denied') &&
            code.contains('Clipboard access was not granted to this mini app.'),
      ),
      isTrue,
    );
  });

  testWidgets('returns a bridge error when profile access was not granted', (
    tester,
  ) async {
    final (fakeRepo, controller, _) = await pumpSandbox(
      tester,
      isAuthenticated: true,
      grantedPermissions: const [],
    );

    controller.dispatchJavaScriptMessage(
      'PostbookBridge',
      jsonEncode({
        'request_id': 'req-profile-denied',
        'type': 'user.profile.read',
        'payload': <String, dynamic>{},
      }),
    );
    await tester.pumpAndSettle();

    expect(fakeRepo.sessionRequests, 0);
    expect(
      controller.executedJavaScript.any(
        (code) =>
            code.contains('req-profile-denied') &&
            code.contains('Profile access was not granted to this mini app.'),
      ),
      isTrue,
    );
  });

  testWidgets(
    'returns a bridge error when session is requested while signed out',
    (tester) async {
      final (fakeRepo, controller, _) = await pumpSandbox(
        tester,
        isAuthenticated: false,
      );

      controller.dispatchJavaScriptMessage(
        'PostbookBridge',
        jsonEncode({
          'request_id': 'req-2',
          'type': 'session.get',
          'payload': <String, dynamic>{},
        }),
      );
      await tester.pumpAndSettle();

      expect(fakeRepo.sessionRequests, 0);
      expect(
        controller.executedJavaScript.any(
          (code) =>
              code.contains('req-2') &&
              code.contains(
                'You need to be signed in to request a mini app session.',
              ),
        ),
        isTrue,
      );
    },
  );
}
