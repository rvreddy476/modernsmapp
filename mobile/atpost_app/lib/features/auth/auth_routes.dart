import 'package:atpost_app/features/auth/anomaly_stepup_screen.dart';
import 'package:atpost_app/features/auth/forgot_password_screen.dart';
import 'package:atpost_app/features/auth/login_screen.dart';
import 'package:atpost_app/features/auth/otp_verify_screen.dart';
import 'package:atpost_app/features/auth/register_screen.dart';
import 'package:go_router/go_router.dart';

class AuthRoutes {
  static List<RouteBase> get routes => [
        GoRoute(
          path: '/login',
          builder: (context, state) => const LoginScreen(),
        ),
        GoRoute(
          path: '/register',
          builder: (context, state) => const RegisterScreen(),
        ),
        GoRoute(
          path: '/forgot-password',
          builder: (context, state) => const ForgotPasswordScreen(),
        ),
        GoRoute(
          path: '/verify-otp',
          builder: (context, state) => OtpVerifyScreen(
            identifier: state.uri.queryParameters['id'] ?? '',
            mode: state.uri.queryParameters['mode'] ?? 'login',
            // Set only by the signup flow; its presence selects the email
            // endpoint over the retired SMS one.
            verificationToken: state.uri.queryParameters['vt'] ?? '',
          ),
        ),
        GoRoute(
          path: '/auth/step-up',
          builder: (context, state) => AnomalyStepUpScreen(
            pendingToken: state.uri.queryParameters['token'] ?? '',
            methods: (state.uri.queryParameters['methods'] ?? '')
                .split(',')
                .where((s) => s.isNotEmpty)
                .toList(),
          ),
        ),
      ];
}
