import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

// ---------------------------------------------------------------------------
// MiniAppsScreen
// ---------------------------------------------------------------------------

class MiniAppsScreen extends ConsumerStatefulWidget {
  const MiniAppsScreen({super.key});

  @override
  ConsumerState<MiniAppsScreen> createState() => _MiniAppsScreenState();
}

class _MiniAppsScreenState extends ConsumerState<MiniAppsScreen> {
  static const _categoryLabels = [
    'Games',
    'Booking',
    'Learning',
    'Shopping',
    'Tools',
    'Entertainment',
  ];
  static const _categoryValues = [
    'games',
    'booking',
    'learning',
    'shopping',
    'tools',
    'entertainment',
  ];

  String? _selectedCategory;
  bool _showInstalled = false;
  List<Map<String, dynamic>> _apps = [];
  List<Map<String, dynamic>> _installedApps = [];
  final Set<String> _loadingIds = {};
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _loadData();
  }

  Future<void> _loadData() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });
    try {
      final params = <String, dynamic>{'limit': 20};
      if (_selectedCategory != null) params['category'] = _selectedCategory;
      final appsRes = await ref
          .read(apiClientProvider)
          .get('/v1/apps', queryParameters: params);
      final installedRes =
          await ref.read(apiClientProvider).get('/v1/apps/installed');

      final appsData = appsRes.data['data'] ?? appsRes.data;
      final installedData =
          installedRes.data['data'] ?? installedRes.data;

      setState(() {
        _apps = List<Map<String, dynamic>>.from(
          appsData is List ? appsData : (appsData['items'] ?? []),
        );
        _installedApps = List<Map<String, dynamic>>.from(
          installedData is List
              ? installedData
              : (installedData['items'] ?? []),
        );
        _isLoading = false;
      });
    } catch (e) {
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  Future<void> _installApp(String appId) async {
    setState(() => _loadingIds.add(appId));
    try {
      await ref.read(apiClientProvider).post(
        '/v1/apps/$appId/install',
        data: {'granted_permissions': <String>[]},
      );
      await _loadData();
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
      if (mounted) setState(() => _loadingIds.remove(appId));
    }
  }

  Future<void> _uninstallApp(String appId) async {
    setState(() => _loadingIds.add(appId));
    try {
      await ref.read(apiClientProvider).delete('/v1/apps/$appId/install');
      await _loadData();
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
      if (mounted) setState(() => _loadingIds.remove(appId));
    }
  }

  Set<String> get _installedIds =>
      _installedApps.map((a) => a['id']?.toString() ?? '').toSet();

  List<Map<String, dynamic>> get _displayedApps {
    if (_showInstalled) {
      final ids = _installedIds;
      return _apps.where((a) => ids.contains(a['id']?.toString() ?? '')).toList();
    }
    return _apps;
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        foregroundColor: AppColors.textPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new, color: AppColors.textPrimary),
          onPressed: () => context.pop(),
        ),
        title: Text('Mini Apps', style: AppTextStyles.h2),
        actions: [
          IconButton(
            icon: Badge(
              isLabelVisible: _installedApps.isNotEmpty,
              label: Text('${_installedApps.length}'),
              child: const Icon(Icons.download_done_outlined),
            ),
            onPressed: () => setState(() => _showInstalled = !_showInstalled),
          ),
        ],
      ),
      body: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Category filter bar
          SizedBox(
            height: 48,
            child: ListView.builder(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 12),
              itemCount: _categoryLabels.length + 1,
              itemBuilder: (context, index) {
                if (index == 0) {
                  final selected = _selectedCategory == null;
                  return Padding(
                    padding: const EdgeInsets.only(right: 8),
                    child: FilterChip(
                      label: const Text('All'),
                      selected: selected,
                      onSelected: (_) {
                        setState(() => _selectedCategory = null);
                        _loadData();
                      },
                      selectedColor: AppColors.postbookPrimary.withValues(alpha: 0.25),
                      checkmarkColor: AppColors.postbookPrimary,
                      labelStyle: AppTextStyles.labelSmall.copyWith(
                        color: selected
                            ? AppColors.postbookPrimary
                            : AppColors.textSecondary,
                      ),
                      side: BorderSide(
                        color: selected
                            ? AppColors.postbookPrimary
                            : AppColors.borderSubtle,
                      ),
                    ),
                  );
                }
                final i = index - 1;
                final value = _categoryValues[i];
                final label = _categoryLabels[i];
                final selected = _selectedCategory == value;
                return Padding(
                  padding: const EdgeInsets.only(right: 8),
                  child: FilterChip(
                    label: Text(label),
                    selected: selected,
                    onSelected: (_) {
                      setState(() =>
                          _selectedCategory = selected ? null : value);
                      _loadData();
                    },
                    selectedColor:
                        AppColors.postbookPrimary.withValues(alpha: 0.25),
                    checkmarkColor: AppColors.postbookPrimary,
                    labelStyle: AppTextStyles.labelSmall.copyWith(
                      color: selected
                          ? AppColors.postbookPrimary
                          : AppColors.textSecondary,
                    ),
                    side: BorderSide(
                      color: selected
                          ? AppColors.postbookPrimary
                          : AppColors.borderSubtle,
                    ),
                  ),
                );
              },
            ),
          ),
          // Content area
          Expanded(
            child: _isLoading
                ? const Center(child: CircularProgressIndicator())
                : _error != null
                    ? Center(
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Icon(Icons.error_outline,
                                color: AppColors.textMuted, size: 48),
                            const SizedBox(height: 12),
                            Text('Failed to load apps',
                                style: AppTextStyles.bodySmall),
                            const SizedBox(height: 8),
                            TextButton(
                              onPressed: _loadData,
                              child: const Text('Retry'),
                            ),
                          ],
                        ),
                      )
                    : _displayedApps.isEmpty
                        ? Center(
                            child: Text(
                              _showInstalled
                                  ? 'No installed apps'
                                  : 'No apps found',
                              style: AppTextStyles.bodySmall,
                            ),
                          )
                        : GridView.builder(
                            padding: const EdgeInsets.all(12),
                            gridDelegate:
                                const SliverGridDelegateWithFixedCrossAxisCount(
                              crossAxisCount: 2,
                              mainAxisSpacing: 12,
                              crossAxisSpacing: 12,
                              childAspectRatio: 0.72,
                            ),
                            itemCount: _displayedApps.length,
                            itemBuilder: (context, i) {
                              final app = _displayedApps[i];
                              final appId = app['id']?.toString() ?? '';
                              final isInstalled =
                                  _installedIds.contains(appId);
                              final isLoading = _loadingIds.contains(appId);
                              return GestureDetector(
                                onTap: appId.isNotEmpty
                                    ? () => context.push('/apps/$appId')
                                    : null,
                                child: _AppCard(
                                  app: app,
                                  isInstalled: isInstalled,
                                  isLoading: isLoading,
                                  onInstall: () => _installApp(appId),
                                  onUninstall: () => _uninstallApp(appId),
                                ),
                              );
                            },
                          ),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// _AppCard
// ---------------------------------------------------------------------------

class _AppCard extends StatelessWidget {
  const _AppCard({
    required this.app,
    required this.isInstalled,
    required this.isLoading,
    required this.onInstall,
    required this.onUninstall,
  });

  final Map<String, dynamic> app;
  final bool isInstalled;
  final bool isLoading;
  final VoidCallback onInstall;
  final VoidCallback onUninstall;

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
    final n =
        count is int ? count : int.tryParse(count.toString()) ?? 0;
    if (n >= 1000) return '${(n / 1000).toStringAsFixed(1)}k';
    return n.toString();
  }

  @override
  Widget build(BuildContext context) {
    final name = app['name'] as String? ?? '';
    final description = app['description']?.toString() ?? '';
    final category = app['category']?.toString();
    final installCount = app['install_count'];

    return Card(
      color: AppColors.bgCard,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: const BorderSide(color: AppColors.borderSubtle),
      ),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // App icon
            CircleAvatar(
              radius: 28,
              backgroundColor:
                  name.isNotEmpty ? _colorForName(name) : Colors.grey,
              child: Text(
                name.isNotEmpty
                    ? name.substring(0, 1).toUpperCase()
                    : '?',
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 22,
                  fontWeight: FontWeight.bold,
                ),
              ),
            ),
            const SizedBox(height: 8),
            // Name
            Text(
              name,
              style: AppTextStyles.label
                  .copyWith(fontWeight: FontWeight.bold),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
            const SizedBox(height: 4),
            // Description
            Text(
              description,
              style: TextStyle(
                fontSize: 12,
                color: Colors.grey[600],
              ),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
            const Spacer(),
            // Category chip
            if (category != null && category.isNotEmpty)
              Chip(
                label: Text(
                  category,
                  style: const TextStyle(fontSize: 10),
                ),
                padding: EdgeInsets.zero,
                materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
            const SizedBox(height: 4),
            // Install count
            Text(
              '${_formatCount(installCount ?? 0)} installs',
              style: const TextStyle(fontSize: 11, color: Colors.grey),
            ),
            const SizedBox(height: 8),
            // Install / Uninstall button
            SizedBox(
              width: double.infinity,
              child: isLoading
                  ? const Center(
                      child: SizedBox(
                        width: 20,
                        height: 20,
                        child:
                            CircularProgressIndicator(strokeWidth: 2),
                      ),
                    )
                  : isInstalled
                      ? OutlinedButton(
                          onPressed: onUninstall,
                          style: OutlinedButton.styleFrom(
                            foregroundColor: const Color(0xFFD8103F),
                            side: const BorderSide(
                                color: Color(0xFFD8103F)),
                          ),
                          child: const Text(
                            'Uninstall',
                            style: TextStyle(fontSize: 12),
                          ),
                        )
                      : ElevatedButton(
                          onPressed: onInstall,
                          style: ElevatedButton.styleFrom(
                            backgroundColor: const Color(0xFFD8103F),
                            foregroundColor: Colors.white,
                          ),
                          child: const Text(
                            'Install',
                            style: TextStyle(fontSize: 12),
                          ),
                        ),
            ),
          ],
        ),
      ),
    );
  }
}
