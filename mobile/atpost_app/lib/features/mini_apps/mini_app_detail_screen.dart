import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mini_app.dart';
import 'package:atpost_app/data/repositories/mini_apps_repository.dart';
import 'package:atpost_app/features/mini_apps/mini_app_permission_prompt.dart';
import 'package:atpost_app/features/mini_apps/mini_app_permissions.dart';
import 'package:atpost_app/providers/mini_apps_provider.dart';
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
  MiniApp? _app;
  bool _isInstalled = false;
  bool _isLoading = true;
  bool _actionLoading = false;
  String? _error;
  List<String> _grantedPermissions = const [];

  @override
  void initState() {
    super.initState();
    _loadApp();
  }

  Future<void> _loadApp() async {
    setState(() {
      _isLoading = true;
      _error = null;
      _app = null;
      _isInstalled = false;
      _grantedPermissions = const [];
    });
    try {
      final app = await ref
          .read(miniAppsRepositoryProvider)
          .getAppWithInstallationState(widget.appId);
      if (!mounted) return;
      setState(() {
        _app = app;
        _isInstalled = app.isInstalled;
        _grantedPermissions = app.grantedPermissions;
        _isLoading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  Future<void> _installApp() async {
    final app = _app;
    if (app == null) return;

    final grantedPermissions = await showMiniAppPermissionPrompt(
      context: context,
      appName: app.name,
      requestedPermissions: app.permissions,
      initiallyGrantedPermissions: _grantedPermissions,
    );
    if (grantedPermissions == null) return;

    setState(() => _actionLoading = true);
    try {
      final didInstall = await ref
          .read(miniAppsProvider.notifier)
          .installApp(widget.appId, grantedPermissions: grantedPermissions);
      if (!mounted) return;
      if (!didInstall) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('Install failed')));
        return;
      }
      setState(() {
        _app = app.copyWith(
          isInstalled: true,
          installCount: app.installCount + 1,
          grantedPermissions: grantedPermissions,
        );
        _isInstalled = true;
        _grantedPermissions = grantedPermissions;
      });
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('App installed!')));
    } finally {
      if (mounted) setState(() => _actionLoading = false);
    }
  }

  Future<void> _uninstallApp() async {
    final app = _app;
    if (app == null) return;

    setState(() => _actionLoading = true);
    try {
      final didUninstall = await ref
          .read(miniAppsProvider.notifier)
          .uninstallApp(widget.appId);
      if (!mounted) return;
      if (!didUninstall) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('Uninstall failed')));
        return;
      }
      setState(() {
        _app = app.copyWith(isInstalled: false, grantedPermissions: const []);
        _isInstalled = false;
        _grantedPermissions = const [];
      });
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('App uninstalled')));
    } finally {
      if (mounted) setState(() => _actionLoading = false);
    }
  }

  void _openApp() {
    context.push('/apps/sandbox/${widget.appId}');
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
    final appName = _app?.name ?? 'App Details';

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        foregroundColor: AppColors.textPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new,
            color: AppColors.textPrimary,
          ),
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
                  const Icon(
                    Icons.error_outline,
                    color: AppColors.textMuted,
                    size: 48,
                  ),
                  const SizedBox(height: 12),
                  Text(
                    'Failed to load app details',
                    style: AppTextStyles.bodySmall,
                  ),
                  const SizedBox(height: 8),
                  TextButton(onPressed: _loadApp, child: const Text('Retry')),
                ],
              ),
            )
          : _app == null
          ? Center(child: Text('App not found', style: AppTextStyles.bodySmall))
          : _AppDetailBody(
              app: _app!,
              isInstalled: _isInstalled,
              actionLoading: _actionLoading,
              onOpen: _openApp,
              onInstall: _installApp,
              onUninstall: _uninstallApp,
              grantedPermissions: _grantedPermissions,
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
    required this.onOpen,
    required this.onInstall,
    required this.onUninstall,
    required this.grantedPermissions,
    required this.colorForName,
    required this.formatCount,
  });

  final MiniApp app;
  final bool isInstalled;
  final bool actionLoading;
  final VoidCallback onOpen;
  final VoidCallback onInstall;
  final VoidCallback onUninstall;
  final List<String> grantedPermissions;
  final Color Function(String) colorForName;
  final String Function(int) formatCount;

  @override
  Widget build(BuildContext context) {
    final name = app.name;
    final description = app.description;
    final category = app.category;
    final installCount = app.installCount;
    final permissions = app.permissions;

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
                backgroundColor: name.isNotEmpty
                    ? colorForName(name)
                    : Colors.grey,
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
                    if (category != null && category.isNotEmpty) ...[
                      const SizedBox(height: 8),
                      Chip(
                        label: Text(
                          category,
                          style: const TextStyle(fontSize: 11),
                        ),
                        padding: EdgeInsets.zero,
                        materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
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
                label: '${formatCount(installCount)} installs',
              ),
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
                ? Column(
                    children: [
                      SizedBox(
                        width: double.infinity,
                        child: ElevatedButton(
                          onPressed: onOpen,
                          style: ElevatedButton.styleFrom(
                            backgroundColor: const Color(0xFFD8103F),
                            foregroundColor: Colors.white,
                            padding: const EdgeInsets.symmetric(vertical: 14),
                          ),
                          child: const Text('Open'),
                        ),
                      ),
                      const SizedBox(height: 12),
                      SizedBox(
                        width: double.infinity,
                        child: OutlinedButton(
                          onPressed: onUninstall,
                          style: OutlinedButton.styleFrom(
                            foregroundColor: const Color(0xFFD8103F),
                            side: const BorderSide(color: Color(0xFFD8103F)),
                            padding: const EdgeInsets.symmetric(vertical: 14),
                          ),
                          child: const Text('Uninstall'),
                        ),
                      ),
                    ],
                  )
                : ElevatedButton(
                    onPressed: onInstall,
                    style: ElevatedButton.styleFrom(
                      backgroundColor: const Color(0xFFD8103F),
                      foregroundColor: Colors.white,
                      padding: const EdgeInsets.symmetric(vertical: 14),
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
                child: Container(
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: AppColors.bgSecondary,
                    borderRadius: BorderRadius.circular(14),
                    border: Border.all(color: AppColors.borderSubtle),
                  ),
                  child: Builder(
                    builder: (context) {
                      final definition = miniAppPermissionFor(perm);
                      final isGranted = grantedPermissions.contains(perm);

                      return Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          const Icon(
                            Icons.lock_outline,
                            size: 16,
                            color: AppColors.textMuted,
                          ),
                          const SizedBox(width: 8),
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Row(
                                  children: [
                                    Expanded(
                                      child: Text(
                                        definition.title,
                                        style: AppTextStyles.label,
                                      ),
                                    ),
                                    if (isInstalled)
                                      Container(
                                        padding: const EdgeInsets.symmetric(
                                          horizontal: 8,
                                          vertical: 4,
                                        ),
                                        decoration: BoxDecoration(
                                          color: isGranted
                                              ? AppColors.postbookPrimary
                                                    .withValues(alpha: 0.12)
                                              : AppColors.borderSubtle,
                                          borderRadius: BorderRadius.circular(
                                            999,
                                          ),
                                        ),
                                        child: Text(
                                          isGranted ? 'Granted' : 'Denied',
                                          style: AppTextStyles.labelTiny
                                              .copyWith(
                                                color: isGranted
                                                    ? AppColors.postbookPrimary
                                                    : AppColors.textMuted,
                                              ),
                                        ),
                                      ),
                                  ],
                                ),
                                const SizedBox(height: 4),
                                Text(
                                  definition.description,
                                  style: AppTextStyles.bodySmall,
                                ),
                                if (definition.key != definition.title) ...[
                                  const SizedBox(height: 4),
                                  Text(
                                    definition.key,
                                    style: AppTextStyles.labelTiny.copyWith(
                                      color: AppColors.textMuted,
                                    ),
                                  ),
                                ],
                              ],
                            ),
                          ),
                        ],
                      );
                    },
                  ),
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
        Text(
          label,
          style: AppTextStyles.bodySmall.copyWith(
            color: AppColors.textTertiary,
          ),
        ),
      ],
    );
  }
}
