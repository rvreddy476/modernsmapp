import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

// ---------------------------------------------------------------------------
// MiniAppDetailScreen
// ---------------------------------------------------------------------------

class MiniAppDetailScreen extends ConsumerStatefulWidget {
  const MiniAppDetailScreen({super.key, required this.appId});

  final String appId;

  @override
  ConsumerState<MiniAppDetailScreen> createState() =>
      _MiniAppDetailScreenState();
}

class _MiniAppDetailScreenState extends ConsumerState<MiniAppDetailScreen> {
  Map<String, dynamic>? _app;
  bool _isInstalled = false;
  bool _isLoading = true;
  bool _actionLoading = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _loadApp();
  }

  Future<void> _loadApp() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });
    try {
      final api = ref.read(apiClientProvider);
      final appRes = await api.get('/v1/apps/${widget.appId}');
      // Also check installed status
      bool installed = false;
      try {
        final installedRes = await api.get('/v1/apps/installed');
        final installedData =
            installedRes.data['data'] ?? installedRes.data;
        final items = installedData is List
            ? installedData
            : (installedData['items'] ?? []) as List<dynamic>;
        installed = items.any(
          (a) => (a as Map<String, dynamic>)['id']?.toString() ==
              widget.appId,
        );
      } catch (_) {
        // installed remains false if this call fails
      }

      final appData = appRes.data['data'] ?? appRes.data;
      setState(() {
        _app = Map<String, dynamic>.from(
          appData is Map ? appData : {},
        );
        _isInstalled = installed;
        _isLoading = false;
      });
    } catch (e) {
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  Future<void> _installApp() async {
    setState(() => _actionLoading = true);
    try {
      await ref.read(apiClientProvider).post(
        '/v1/apps/${widget.appId}/install',
        data: {'granted_permissions': <String>[]},
      );
      setState(() => _isInstalled = true);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('App installed!')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Install failed: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _actionLoading = false);
    }
  }

  Future<void> _uninstallApp() async {
    setState(() => _actionLoading = true);
    try {
      await ref
          .read(apiClientProvider)
          .delete('/v1/apps/${widget.appId}/install');
      setState(() => _isInstalled = false);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('App uninstalled')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Uninstall failed: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _actionLoading = false);
    }
  }

  Color _colorForName(String name) {
    const colors = [
      Colors.blue,
      Colors.green,
      Colors.orange,
      Colors.purple,
      Colors.teal,
      Colors.indigo,
    ];
    return colors[name.hashCode.abs() % colors.length];
  }

  String _formatCount(dynamic count) {
    final n = count is int ? count : int.tryParse(count.toString()) ?? 0;
    if (n >= 1000) return '${(n / 1000).toStringAsFixed(1)}k';
    return n.toString();
  }

  @override
  Widget build(BuildContext context) {
    final appName = _app?['name']?.toString() ?? 'App Details';

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        foregroundColor: AppColors.textPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new,
              color: AppColors.textPrimary),
          onPressed: () => context.pop(),
        ),
        title: Text(
          _isLoading ? 'App Details' : appName,
          style: AppTextStyles.h2,
        ),
      ),
      body: _isLoading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Icon(Icons.error_outline,
                          color: AppColors.textMuted, size: 48),
                      const SizedBox(height: 12),
                      Text('Failed to load app details',
                          style: AppTextStyles.bodySmall),
                      const SizedBox(height: 8),
                      TextButton(
                        onPressed: _loadApp,
                        child: const Text('Retry'),
                      ),
                    ],
                  ),
                )
              : _app == null
                  ? Center(
                      child: Text('App not found',
                          style: AppTextStyles.bodySmall))
                  : _AppDetailBody(
                      app: _app!,
                      isInstalled: _isInstalled,
                      actionLoading: _actionLoading,
                      onInstall: _installApp,
                      onUninstall: _uninstallApp,
                      colorForName: _colorForName,
                      formatCount: _formatCount,
                    ),
    );
  }
}

// ---------------------------------------------------------------------------
// _AppDetailBody
// ---------------------------------------------------------------------------

class _AppDetailBody extends StatelessWidget {
  const _AppDetailBody({
    required this.app,
    required this.isInstalled,
    required this.actionLoading,
    required this.onInstall,
    required this.onUninstall,
    required this.colorForName,
    required this.formatCount,
  });

  final Map<String, dynamic> app;
  final bool isInstalled;
  final bool actionLoading;
  final VoidCallback onInstall;
  final VoidCallback onUninstall;
  final Color Function(String) colorForName;
  final String Function(dynamic) formatCount;

  @override
  Widget build(BuildContext context) {
    final name = app['name']?.toString() ?? '';
    final description = app['description']?.toString() ?? '';
    final category = app['category']?.toString();
    final installCount = app['install_count'];
    final permissions =
        (app['permissions'] as List<dynamic>?)?.map((p) => p.toString()).toList() ??
            [];
    final version = app['version']?.toString();
    final developer = app['developer']?.toString() ?? app['author']?.toString();

    return SingleChildScrollView(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Header: icon + name + category
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              CircleAvatar(
                radius: 40,
                backgroundColor:
                    name.isNotEmpty ? colorForName(name) : Colors.grey,
                child: Text(
                  name.isNotEmpty ? name.substring(0, 1).toUpperCase() : '?',
                  style: const TextStyle(
                    color: Colors.white,
                    fontSize: 32,
                    fontWeight: FontWeight.bold,
                  ),
                ),
              ),
              const SizedBox(width: 16),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(name, style: AppTextStyles.h2),
                    if (developer != null && developer.isNotEmpty) ...[
                      const SizedBox(height: 4),
                      Text(
                        developer,
                        style: AppTextStyles.bodySmall
                            .copyWith(color: AppColors.textTertiary),
                      ),
                    ],
                    if (category != null && category.isNotEmpty) ...[
                      const SizedBox(height: 8),
                      Chip(
                        label: Text(
                          category,
                          style: const TextStyle(fontSize: 11),
                        ),
                        padding: EdgeInsets.zero,
                        materialTapTargetSize:
                            MaterialTapTargetSize.shrinkWrap,
                      ),
                    ],
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 24),

          // Stats row
          Row(
            children: [
              _StatItem(
                icon: Icons.download_outlined,
                label: '${formatCount(installCount ?? 0)} installs',
              ),
              if (version != null && version.isNotEmpty) ...[
                const SizedBox(width: 24),
                _StatItem(
                  icon: Icons.info_outline,
                  label: 'v$version',
                ),
              ],
            ],
          ),
          const SizedBox(height: 24),

          // Install / Uninstall button
          SizedBox(
            width: double.infinity,
            child: actionLoading
                ? const Center(
                    child: SizedBox(
                      width: 24,
                      height: 24,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    ),
                  )
                : isInstalled
                    ? OutlinedButton(
                        onPressed: onUninstall,
                        style: OutlinedButton.styleFrom(
                          foregroundColor: const Color(0xFFD8103F),
                          side: const BorderSide(color: Color(0xFFD8103F)),
                          padding:
                              const EdgeInsets.symmetric(vertical: 14),
                        ),
                        child: const Text('Uninstall'),
                      )
                    : ElevatedButton(
                        onPressed: onInstall,
                        style: ElevatedButton.styleFrom(
                          backgroundColor: const Color(0xFFD8103F),
                          foregroundColor: Colors.white,
                          padding:
                              const EdgeInsets.symmetric(vertical: 14),
                        ),
                        child: const Text('Install'),
                      ),
          ),
          const SizedBox(height: 32),

          // Description
          if (description.isNotEmpty) ...[
            Text('About', style: AppTextStyles.h3),
            const SizedBox(height: 8),
            Text(description, style: AppTextStyles.bodySmall),
            const SizedBox(height: 24),
          ],

          // Permissions
          if (permissions.isNotEmpty) ...[
            Text('Permissions', style: AppTextStyles.h3),
            const SizedBox(height: 8),
            ...permissions.map(
              (perm) => Padding(
                padding: const EdgeInsets.symmetric(vertical: 4),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Icon(
                      Icons.lock_outline,
                      size: 16,
                      color: AppColors.textMuted,
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        perm,
                        style: AppTextStyles.bodySmall,
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// _StatItem helper
// ---------------------------------------------------------------------------

class _StatItem extends StatelessWidget {
  const _StatItem({required this.icon, required this.label});

  final IconData icon;
  final String label;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 16, color: AppColors.textMuted),
        const SizedBox(width: 4),
        Text(label,
            style: AppTextStyles.bodySmall
                .copyWith(color: AppColors.textTertiary)),
      ],
    );
  }
}
