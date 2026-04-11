import 'package:atpost_app/app/router.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:mocktail/mocktail.dart';

class _MockDio extends Mock implements Dio {}

class _MockFlutterSecureStorage extends Mock implements FlutterSecureStorage {}

class _RouterHarness extends ConsumerWidget {
  const _RouterHarness();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return MaterialApp.router(routerConfig: ref.watch(appRouterProvider));
  }
}

AuthService _buildSignedOutAuthService() {
  final storage = _MockFlutterSecureStorage();
  final dio = _MockDio();

  when(
    () => storage.read(key: any(named: 'key')),
  ).thenAnswer((_) async => null);
  when(
    () => storage.write(
      key: any(named: 'key'),
      value: any(named: 'value'),
    ),
  ).thenAnswer((_) async {});
  when(() => storage.delete(key: any(named: 'key'))).thenAnswer((_) async {});

  final auth = AuthService(storage: storage, dio: dio);
  auth.sessionReady = Future.value();
  return auth;
}

Future<(AuthService, GoRouter)> _pumpRouter(
  WidgetTester tester, {
  required AuthService auth,
}) async {
  final container = ProviderContainer(
    overrides: [authServiceProvider.overrideWithValue(auth)],
  );
  addTearDown(container.dispose);

  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: const _RouterHarness(),
    ),
  );
  await tester.pumpAndSettle();

  return (auth, container.read(appRouterProvider));
}

void main() {
  group('appRouterProvider', () {
    testWidgets(
      'redirects splash to login when the session is not authenticated',
      (tester) async {
        await _pumpRouter(tester, auth: _buildSignedOutAuthService());

        expect(find.text('Welcome back'), findsOneWidget);
        expect(find.text('Sign in to continue'), findsOneWidget);
      },
    );

    testWidgets('redirects protected routes to login when signed out', (
      tester,
    ) async {
      final (_, router) = await _pumpRouter(
        tester,
        auth: _buildSignedOutAuthService(),
      );

      router.go('/chat');
      await tester.pumpAndSettle();

      expect(find.text('Welcome back'), findsOneWidget);
      expect(find.text('Sign in to continue'), findsOneWidget);
    });

    testWidgets('allows public routes when signed out', (tester) async {
      final (_, router) = await _pumpRouter(
        tester,
        auth: _buildSignedOutAuthService(),
      );

      router.go('/forgot-password');
      await tester.pumpAndSettle();

      expect(find.text('Forgot your password?'), findsOneWidget);
      expect(find.text('Send Reset Code'), findsOneWidget);
    });
  });
}
