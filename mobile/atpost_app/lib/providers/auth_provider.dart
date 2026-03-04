import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Convenience provider: true if user has a valid session token.
final isAuthenticatedProvider = Provider<bool>((ref) {
  final authAsync = ref.watch(authStateProvider);
  return authAsync.valueOrNull?.isAuthenticated ?? false;
});
