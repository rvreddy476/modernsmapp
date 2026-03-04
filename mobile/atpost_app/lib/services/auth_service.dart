import 'dart:async';

import 'package:atpost_app/core/config/environment.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Lightweight auth state.
class AuthState {
  final String? userId;
  final String? token;
  final String? refreshToken;
  final bool isAuthenticated;

  const AuthState({
    this.userId,
    this.token,
    this.refreshToken,
    this.isAuthenticated = false,
  });

  AuthState copyWith({
    String? userId,
    String? token,
    String? refreshToken,
    bool? isAuthenticated,
  }) {
    return AuthState(
      userId: userId ?? this.userId,
      token: token ?? this.token,
      refreshToken: refreshToken ?? this.refreshToken,
      isAuthenticated: isAuthenticated ?? this.isAuthenticated,
    );
  }
}

/// Manages authentication tokens and user session.
class AuthService {
  final _stateController = StreamController<AuthState>.broadcast();
  AuthState _state = const AuthState();

  Stream<AuthState> get stateStream => _stateController.stream;
  AuthState get state => _state;
  String? get token => _state.token;
  String? get userId => _state.userId;
  bool get isAuthenticated => _state.isAuthenticated;

  final Dio _dio = Dio(BaseOptions(
    baseUrl: Environment.apiBaseUrl,
    connectTimeout: const Duration(seconds: 10),
  ));

  /// Login with phone/email and password.
  Future<bool> login(String identifier, String password) async {
    try {
      final response = await _dio.post('${Environment.authPath}/login', data: {
        'identifier': identifier,
        'password': password,
      });

      final data = response.data['data'] as Map<String, dynamic>?;
      if (data != null) {
        _state = AuthState(
          userId: data['user_id'] as String?,
          token: data['access_token'] as String?,
          refreshToken: data['refresh_token'] as String?,
          isAuthenticated: true,
        );
        _stateController.add(_state);
        return true;
      }
    } catch (_) {
      // Login failed
    }
    return false;
  }

  /// Set auth state directly (e.g. from stored credentials).
  void setSession({
    required String userId,
    required String token,
    String? refreshToken,
  }) {
    _state = AuthState(
      userId: userId,
      token: token,
      refreshToken: refreshToken,
      isAuthenticated: true,
    );
    _stateController.add(_state);
  }

  /// Export all user data for GDPR portability.
  /// Returns the raw response data as a map.
  Future<Map<String, dynamic>> exportUserData() async {
    final response = await _dio.get(
      '${Environment.authPath}/data-export',
      options: Options(headers: {
        if (_state.token != null) 'Authorization': 'Bearer ${_state.token}',
      }),
    );
    return Map<String, dynamic>.from(response.data);
  }

  /// Clear session.
  void logout() {
    _state = const AuthState();
    _stateController.add(_state);
  }

  void dispose() {
    _stateController.close();
  }
}

/// Global auth service provider.
final authServiceProvider = Provider<AuthService>((ref) {
  final service = AuthService();
  ref.onDispose(service.dispose);
  return service;
});

/// Auth state provider for reactive UI updates.
final authStateProvider = StreamProvider<AuthState>((ref) {
  final auth = ref.watch(authServiceProvider);
  return auth.stateStream;
});
