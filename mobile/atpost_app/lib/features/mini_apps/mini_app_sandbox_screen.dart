import 'dart:convert';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/data/models/mini_app.dart';
import 'package:atpost_app/data/models/mini_app_manifest.dart';
import 'package:atpost_app/data/repositories/mini_apps_repository.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/providers/mini_apps_provider.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter/services.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';
import 'package:mime/mime.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';
import 'package:webview_flutter/webview_flutter.dart';

class MiniAppSandboxScreen extends ConsumerStatefulWidget {
  final String appId;

  const MiniAppSandboxScreen({super.key, required this.appId});

  @override
  ConsumerState<MiniAppSandboxScreen> createState() =>
      _MiniAppSandboxScreenState();
}

class _MiniAppSandboxScreenState extends ConsumerState<MiniAppSandboxScreen> {
  late final WebViewController _controller;
  final DateTime _launchedAt = DateTime.now();
  final ImagePicker _imagePicker = ImagePicker();
  bool _isResolvingApp = true;
  bool _isLoading = true;
  double _progress = 0;
  MiniApp? _app;
  MiniAppLaunchConfig? _launchConfig;
  String? _appError;
  String? _loadedEntryUrl;
  String? _lastBlockedUrl;
  Map<String, dynamic>? _remoteSession;
  DateTime? _remoteSessionExpiresAt;

  @override
  void initState() {
    super.initState();
    _initWebview();
    _resolveApp();
  }

  void _initWebview() {
    _controller = WebViewController()
      ..setJavaScriptMode(JavaScriptMode.unrestricted)
      ..setBackgroundColor(Colors.black)
      ..addJavaScriptChannel(
        'PostbookBridge',
        onMessageReceived: (message) {
          _handleBridgeMessage(message.message);
        },
      )
      ..setNavigationDelegate(
        NavigationDelegate(
          onProgress: (int progress) {
            if (mounted) setState(() => _progress = progress / 100);
          },
          onPageStarted: (String url) {
            if (mounted) setState(() => _isLoading = true);
          },
          onNavigationRequest: _handleNavigationRequest,
          onPageFinished: (String url) {
            if (mounted) setState(() => _isLoading = false);
            _injectJsBridge();
          },
          onWebResourceError: (WebResourceError error) {
            if (error.isForMainFrame == false || !mounted) return;
            setState(() {
              _appError = 'Failed to load mini app';
              _isLoading = false;
            });
            debugPrint('Webview Error: ${error.description}');
          },
        ),
      );
  }

  void _injectJsBridge() {
    final grantedPermissions = jsonEncode(_app?.grantedPermissions ?? const []);
    final appDescriptor = jsonEncode(_buildAppDescriptor());
    final sessionDescriptor = jsonEncode(_buildSessionDescriptor());

    final js =
        """
      (function() {
        window.__PostbookBridgeRuntime = window.__PostbookBridgeRuntime || {
          pending: {},
          nextId: 1,
          request: function(type, payload) {
            return new Promise(function(resolve, reject) {
              const requestId = String(window.__PostbookBridgeRuntime.nextId++);
              window.__PostbookBridgeRuntime.pending[requestId] = {
                resolve: resolve,
                reject: reject
              };
              PostbookBridge.postMessage(JSON.stringify({
                request_id: requestId,
                type: type,
                payload: payload || {}
              }));
            });
          },
          settle: function(message) {
            const entry = window.__PostbookBridgeRuntime.pending[message.request_id];
            if (!entry) {
              return;
            }
            delete window.__PostbookBridgeRuntime.pending[message.request_id];
            if (message.ok) {
              entry.resolve(message.result);
              return;
            }
            entry.reject(new Error(message.error || 'Mini app host action failed.'));
          }
        };

        window.Postbook = Object.assign(window.Postbook || {}, {
          platform: 'mobile',
          theme: 'dark',
          accentColor: '#FF6B35',
          permissions: $grantedPermissions,
          app: $appDescriptor,
          session: $sessionDescriptor,
          close: function() {
            PostbookBridge.postMessage('close');
          },
          hasPermission: function(permission) {
            return this.permissions.indexOf(permission) >= 0;
          },
          request: function(type, payload) {
            return window.__PostbookBridgeRuntime.request(type, payload);
          },
          copyText: function(text) {
            return this.request('clipboard.write', {
              text: String(text ?? '')
            });
          },
          getProfile: function() {
            return this.request('user.profile.read', {});
          },
          getSession: function() {
            return this.request('session.get', {});
          },
          sendNotification: function(options) {
            return this.request('notifications.send', options || {});
          },
          notify: function(options) {
            return this.request('notifications.send', options || {});
          },
          captureImage: function(options) {
            return this.request('device.camera.capture', options || {});
          },
          requestMicrophone: function() {
            return this.request('device.microphone.request', {});
          }
        });
      })();
    """;
    _controller.runJavaScript(js);
  }

  Future<void> _handleBridgeMessage(String rawMessage) async {
    if (rawMessage == 'close') {
      if (mounted) {
        context.pop();
      }
      return;
    }

    Map<String, dynamic>? payload;
    try {
      final decoded = jsonDecode(rawMessage);
      if (decoded is Map<String, dynamic>) {
        payload = decoded;
      }
    } catch (_) {
      AppLogger.warn(
        'Ignored invalid mini app bridge payload',
        tag: 'MiniAppSandbox',
      );
      return;
    }

    if (payload == null) {
      return;
    }

    final requestId = payload['request_id']?.toString();
    final type = payload['type']?.toString() ?? '';
    final body = _extractBridgePayload(payload);

    try {
      final result = switch (type) {
        'clipboard.write' => await _handleClipboardWrite(body),
        'user.profile.read' => await _handleProfileRead(),
        'session.get' => await _handleSessionGet(),
        'notifications.send' => _handleNotificationSend(body),
        'device.camera.capture' => await _handleCameraCapture(body),
        'device.microphone.request' => await _handleMicrophoneRequest(),
        _ => throw const _MiniAppBridgeException('Unsupported mini app action'),
      };
      await _sendBridgeResponse(requestId, result: result);
    } catch (e, st) {
      final message = _bridgeErrorMessage(e);
      AppLogger.warn(
        'Mini app bridge action failed: $type',
        tag: 'MiniAppSandbox',
        error: e,
        stackTrace: st,
      );
      await _sendBridgeResponse(requestId, error: message);
      if (requestId == null || requestId.isEmpty) {
        _showSnackBar(message);
      }
    }
  }

  Future<Map<String, dynamic>> _handleClipboardWrite(
    Map<String, dynamic> payload,
  ) async {
    if (!_hasPermission('clipboard.write')) {
      throw const _MiniAppBridgeException(
        'Clipboard access was not granted to this mini app.',
      );
    }

    final text = payload['text']?.toString() ?? '';
    if (text.isEmpty) {
      throw const _MiniAppBridgeException('Nothing to copy.');
    }

    await Clipboard.setData(ClipboardData(text: text));
    _showSnackBar('Copied to clipboard');
    return {'copied': true, 'length': text.length};
  }

  Future<Map<String, dynamic>> _handleProfileRead() async {
    if (!_hasPermission('user.profile.read')) {
      throw const _MiniAppBridgeException(
        'Profile access was not granted to this mini app.',
      );
    }

    final user = await ref.read(userRepositoryProvider).getMe();
    return {
      'id': user.id,
      'username': user.username,
      'display_name': user.displayName,
      'bio': user.bio,
      'pronouns': user.pronouns,
      'avatar_url': user.avatarUrl,
      'location': user.location,
      'profession': user.profession,
      'is_verified': user.isVerified,
      'follower_count': user.followerCount,
      'following_count': user.followingCount,
      'friend_count': user.friendCount,
    };
  }

  Future<Map<String, dynamic>> _handleSessionGet() async {
    final auth = ref.read(authServiceProvider);
    if (!auth.isAuthenticated) {
      throw const _MiniAppBridgeException(
        'You need to be signed in to request a mini app session.',
      );
    }

    final cachedSession = _remoteSession;
    final expiresAt = _remoteSessionExpiresAt;
    final now = DateTime.now().toUtc();
    if (cachedSession != null &&
        expiresAt != null &&
        expiresAt.isAfter(now.add(const Duration(seconds: 30)))) {
      return {..._buildSessionDescriptor(), ...cachedSession};
    }

    final session = await ref
        .read(miniAppsRepositoryProvider)
        .getAppSession(widget.appId);
    _remoteSession = session;
    final expiresAtRaw = session['expires_at']?.toString();
    _remoteSessionExpiresAt = expiresAtRaw == null
        ? null
        : DateTime.tryParse(expiresAtRaw)?.toUtc();

    return {..._buildSessionDescriptor(), ...session};
  }

  Map<String, dynamic> _handleNotificationSend(Map<String, dynamic> payload) {
    if (!_hasPermission('notifications.send')) {
      throw const _MiniAppBridgeException(
        'Notification access was not granted to this mini app.',
      );
    }

    final title = _stringValue(payload['title']);
    final body = _stringValue(payload['body']);
    final message = _stringValue(payload['message']);
    final notificationBody = body.isNotEmpty ? body : message;

    if (title.isEmpty && notificationBody.isEmpty) {
      throw const _MiniAppBridgeException('Notification content is empty.');
    }

    final duration = Duration(
      milliseconds: _clampInt(
        payload['duration_ms'],
        fallback: 4000,
        min: 1000,
        max: 12000,
      ),
    );

    final text = title.isNotEmpty && notificationBody.isNotEmpty
        ? '$title\n$notificationBody'
        : (title.isNotEmpty ? title : notificationBody);

    _showSnackBar(text, duration: duration);
    return {
      'displayed': true,
      'title': title,
      'body': notificationBody,
      'duration_ms': duration.inMilliseconds,
    };
  }

  Future<Map<String, dynamic>> _handleCameraCapture(
    Map<String, dynamic> payload,
  ) async {
    if (!_hasPermission('device.camera')) {
      throw const _MiniAppBridgeException(
        'Camera access was not granted to this mini app.',
      );
    }

    final preferredCamera =
        _stringValue(payload['preferred_camera']).toLowerCase() == 'front'
        ? CameraDevice.front
        : CameraDevice.rear;

    final capture = await _imagePicker.pickImage(
      source: ImageSource.camera,
      preferredCameraDevice: preferredCamera,
      imageQuality: _clampInt(
        payload['image_quality'],
        fallback: 80,
        min: 30,
        max: 100,
      ),
      maxWidth: _positiveDouble(payload['max_width']) ?? 1280,
      maxHeight: _positiveDouble(payload['max_height']) ?? 1280,
    );

    if (capture == null) {
      return {'cancelled': true};
    }

    final bytes = await capture.readAsBytes();
    final mimeType =
        lookupMimeType(
          capture.path,
          headerBytes: bytes.take(12).toList(growable: false),
        ) ??
        'image/jpeg';

    return {
      'cancelled': false,
      'file_name': capture.name,
      'mime_type': mimeType,
      'size_bytes': bytes.length,
      'data_url': 'data:$mimeType;base64,${base64Encode(bytes)}',
    };
  }

  Future<Map<String, dynamic>> _handleMicrophoneRequest() async {
    if (!_hasPermission('device.microphone')) {
      throw const _MiniAppBridgeException(
        'Microphone access was not granted to this mini app.',
      );
    }

    MediaStream? stream;
    try {
      stream = await navigator.mediaDevices.getUserMedia(const {
        'audio': true,
        'video': false,
      });
      return {'granted': true};
    } catch (e, st) {
      AppLogger.warn(
        'Mini app microphone access request failed',
        tag: 'MiniAppSandbox',
        error: e,
        stackTrace: st,
      );
      return {
        'granted': false,
        'error':
            'Microphone access was denied or is unavailable on this device.',
      };
    } finally {
      if (stream != null) {
        for (final track in stream.getTracks()) {
          try {
            await track.stop();
          } catch (_) {}
        }
      }
    }
  }

  bool _hasPermission(String permission) {
    final app = _app;
    if (app == null) return false;
    return app.grantedPermissions.contains(permission);
  }

  Future<void> _sendBridgeResponse(
    String? requestId, {
    Object? result,
    String? error,
  }) async {
    if (requestId == null || requestId.isEmpty) {
      return;
    }

    final payload = jsonEncode({
      'request_id': requestId,
      'ok': error == null,
      if (error == null) 'result': result,
      'error': ?error,
    });

    await _controller.runJavaScript("""
      (function() {
        const runtime = window.__PostbookBridgeRuntime;
        if (!runtime || typeof runtime.settle !== 'function') {
          return;
        }
        runtime.settle($payload);
      })();
    """);
  }

  Map<String, dynamic> _extractBridgePayload(Map<String, dynamic> envelope) {
    final payload = envelope['payload'];
    if (payload is Map<String, dynamic>) {
      return payload;
    }
    if (payload is Map) {
      return payload.map((key, value) => MapEntry(key.toString(), value));
    }
    return envelope;
  }

  Map<String, dynamic> _buildAppDescriptor() {
    final app = _app;
    return {
      'id': app?.id ?? widget.appId,
      'name': app?.name ?? 'Mini App',
      'category': app?.category,
      'manifest_url': app?.manifestUrl,
      'requested_permissions': app?.permissions ?? const <String>[],
      'granted_permissions': app?.grantedPermissions ?? const <String>[],
      'entry_url': _launchConfig?.entryUri.toString(),
    };
  }

  Map<String, dynamic> _buildSessionDescriptor() {
    final auth = ref.read(authServiceProvider);
    return {
      'app_id': widget.appId,
      'user_id': auth.userId,
      'platform': 'mobile',
      'is_authenticated': auth.isAuthenticated,
      'granted_permissions': _app?.grantedPermissions ?? const <String>[],
      'launched_at': _launchedAt.toUtc().toIso8601String(),
    };
  }

  void _showSnackBar(
    String message, {
    Duration duration = const Duration(seconds: 4),
  }) {
    if (!mounted) return;
    final messenger = ScaffoldMessenger.maybeOf(context);
    messenger
      ?..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message), duration: duration));
  }

  NavigationDecision _handleNavigationRequest(NavigationRequest request) {
    final uri = Uri.tryParse(request.url);
    final launchConfig = _launchConfig;
    if (launchConfig == null || launchConfig.allows(uri)) {
      return NavigationDecision.navigate;
    }

    AppLogger.warn(
      'Blocked mini app navigation outside allowed origins: ${request.url}',
      tag: 'MiniAppSandbox',
    );

    if (_lastBlockedUrl != request.url) {
      _lastBlockedUrl = request.url;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        _showSnackBar('Blocked navigation outside this mini app');
      });
    }

    return NavigationDecision.prevent;
  }

  Future<void> _resolveApp() async {
    final state = ref.read(miniAppsProvider);
    final cachedApp =
        state.valueOrNull?.allApps.cast<MiniApp?>().firstWhere(
          (app) => app?.id == widget.appId,
          orElse: () => null,
        ) ??
        state.valueOrNull?.installedApps.cast<MiniApp?>().firstWhere(
          (app) => app?.id == widget.appId,
          orElse: () => null,
        );

    if (cachedApp != null) {
      await _showApp(cachedApp);
      return;
    }

    try {
      final app = await ref
          .read(miniAppsRepositoryProvider)
          .getAppWithInstallationState(widget.appId);
      if (app.id == 'error') {
        if (!mounted) return;
        setState(() {
          _app = null;
          _appError = 'App not found';
          _isResolvingApp = false;
          _isLoading = false;
        });
        return;
      }
      await _showApp(app);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _app = null;
        _appError = 'Failed to load app details';
        _isResolvingApp = false;
        _isLoading = false;
      });
    }
  }

  Future<void> _showApp(MiniApp app) async {
    if (!mounted) return;
    setState(() {
      _app = app;
      _launchConfig = null;
      _appError = null;
      _isResolvingApp = false;
      _isLoading = true;
      _progress = 0;
      _lastBlockedUrl = null;
      _remoteSession = null;
      _remoteSessionExpiresAt = null;
    });
    await _loadMiniApp(app);
  }

  Future<void> _loadMiniApp(MiniApp app) async {
    try {
      final launchConfig = await ref
          .read(miniAppsRepositoryProvider)
          .resolveLaunchConfig(app);
      if (!mounted) return;

      final entryUrl = launchConfig.entryUri.toString();
      setState(() {
        _launchConfig = launchConfig;
        _appError = null;
      });

      if (_loadedEntryUrl == entryUrl) {
        return;
      }

      _loadedEntryUrl = entryUrl;
      await _controller.loadRequest(launchConfig.entryUri);
    } on FormatException catch (e) {
      if (!mounted) return;
      setState(() {
        _appError = e.message;
        _isLoading = false;
      });
    } catch (e) {
      AppLogger.error(
        'Mini app launch failed',
        tag: 'MiniAppSandbox',
        error: e,
      );
      if (!mounted) return;
      setState(() {
        _appError = 'Failed to open mini app';
        _isLoading = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_isResolvingApp) {
      return const Scaffold(body: Center(child: CircularProgressIndicator()));
    }

    if (_appError != null || _app == null) {
      return Scaffold(
        backgroundColor: Colors.black,
        appBar: AppBar(
          backgroundColor: const Color(0xFF1A1D2E),
          leading: IconButton(
            icon: const Icon(Icons.close, color: Colors.white),
            onPressed: () => context.pop(),
          ),
          title: Text(
            'Mini App',
            style: AppTextStyles.label.copyWith(color: Colors.white),
          ),
        ),
        body: Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Icon(
                  Icons.error_outline,
                  color: Colors.white54,
                  size: 40,
                ),
                const SizedBox(height: 12),
                Text(
                  _appError ?? 'App unavailable',
                  style: AppTextStyles.body.copyWith(color: Colors.white),
                  textAlign: TextAlign.center,
                ),
                const SizedBox(height: 12),
                TextButton(
                  onPressed: () {
                    setState(() {
                      _app = null;
                      _appError = null;
                      _isResolvingApp = true;
                      _isLoading = true;
                      _progress = 0;
                      _launchConfig = null;
                      _loadedEntryUrl = null;
                      _lastBlockedUrl = null;
                      _remoteSession = null;
                      _remoteSessionExpiresAt = null;
                    });
                    _resolveApp();
                  },
                  child: const Text('Retry'),
                ),
              ],
            ),
          ),
        ),
      );
    }

    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: const Color(0xFF1A1D2E),
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.close, color: Colors.white),
          onPressed: () => context.pop(),
        ),
        title: Row(
          children: [
            if (_app?.iconUrl != null)
              CircleAvatar(
                radius: 14,
                backgroundImage: NetworkImage(_app!.iconUrl!),
              )
            else
              const CircleAvatar(
                radius: 14,
                backgroundColor: Colors.white10,
                child: Icon(Icons.apps, size: 14),
              ),
            const SizedBox(width: 10),
            Text(
              _app?.name ?? 'Mini App',
              style: AppTextStyles.label.copyWith(color: Colors.white),
            ),
          ],
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh, size: 20),
            onPressed: () async {
              final app = _app;
              if (app == null) return;
              _loadedEntryUrl = null;
              await _loadMiniApp(app);
            },
          ),
          IconButton(icon: const Icon(Icons.more_horiz), onPressed: () {}),
        ],
        bottom: _isLoading
            ? PreferredSize(
                preferredSize: const Size.fromHeight(2),
                child: LinearProgressIndicator(
                  value: _progress,
                  backgroundColor: Colors.transparent,
                  color: AppColors.postbookPrimary,
                  minHeight: 2,
                ),
              )
            : null,
      ),
      body: WebViewWidget(controller: _controller),
    );
  }
}

String _stringValue(dynamic value) {
  if (value == null) {
    return '';
  }
  return value.toString().trim();
}

int _clampInt(
  dynamic value, {
  required int fallback,
  required int min,
  required int max,
}) {
  final parsed = switch (value) {
    int v => v,
    String v => int.tryParse(v),
    _ => null,
  };
  if (parsed == null) {
    return fallback;
  }
  return parsed.clamp(min, max);
}

double? _positiveDouble(dynamic value) {
  final parsed = switch (value) {
    double v => v,
    int v => v.toDouble(),
    String v => double.tryParse(v),
    _ => null,
  };
  if (parsed == null || parsed <= 0) {
    return null;
  }
  return parsed;
}

String _bridgeErrorMessage(Object error) {
  final message = error.toString().trim();
  if (message.startsWith('Exception: ')) {
    return message.substring('Exception: '.length);
  }
  return message;
}

class _MiniAppBridgeException implements Exception {
  final String message;

  const _MiniAppBridgeException(this.message);

  @override
  String toString() => message;
}
