import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class SecuritySettingsScreen extends ConsumerStatefulWidget {
  const SecuritySettingsScreen({super.key});

  @override
  ConsumerState<SecuritySettingsScreen> createState() =>
      _SecuritySettingsScreenState();
}

class _SecuritySettingsScreenState
    extends ConsumerState<SecuritySettingsScreen> {
  final _formKey = GlobalKey<FormState>();
  final _currentPasswordController = TextEditingController();
  final _newPasswordController = TextEditingController();
  final _confirmPasswordController = TextEditingController();

  bool _obscureCurrent = true;
  bool _obscureNew = true;
  bool _obscureConfirm = true;
  bool _savingPassword = false;
  bool _twoFactorEnabled = false;
  bool _togglingTwoFactor = false;
  bool _loadingSessions = false;
  List<_ActiveSession> _sessions = const [];

  bool _loadingAnomalies = false;
  List<_LoginAnomaly> _anomalies = const [];
  final Set<String> _ackingAnomalyIds = <String>{};

  @override
  void initState() {
    super.initState();
    Future.microtask(_loadSessions);
    Future.microtask(_loadAnomalies);
  }

  @override
  void dispose() {
    _currentPasswordController.dispose();
    _newPasswordController.dispose();
    _confirmPasswordController.dispose();
    super.dispose();
  }

  Future<void> _toggleTwoFactor(bool enable) async {
    setState(() => _togglingTwoFactor = true);
    try {
      final endpoint = enable
          ? '${Environment.authPath}/2fa/enable'
          : '${Environment.authPath}/2fa/disable';
      await ref.read(apiClientProvider).post(endpoint);
      if (mounted) setState(() => _twoFactorEnabled = enable);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('Failed to ${enable ? 'enable' : 'disable'} 2FA'),
          ),
        );
      }
    } finally {
      if (mounted) setState(() => _togglingTwoFactor = false);
    }
  }

  Future<void> _changePassword() async {
    if (!(_formKey.currentState?.validate() ?? false)) return;
    setState(() => _savingPassword = true);
    try {
      await ref
          .read(apiClientProvider)
          .post(
            '${Environment.authPath}/change-password',
            data: {
              'current_password': _currentPasswordController.text,
              'new_password': _newPasswordController.text,
            },
          );
      if (mounted) {
        _currentPasswordController.clear();
        _newPasswordController.clear();
        _confirmPasswordController.clear();
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Password changed successfully')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to change password: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _savingPassword = false);
    }
  }

  Future<void> _loadSessions() async {
    if (_loadingSessions) return;
    setState(() => _loadingSessions = true);
    try {
      final response = await ref
          .read(apiClientProvider)
          .get('${Environment.authPath}/sessions');
      final items = (response.data['data'] as List<dynamic>?) ?? [];
      if (!mounted) return;
      setState(() {
        _sessions = items
            .whereType<Map>()
            .map(
              (item) =>
                  _ActiveSession.fromJson(Map<String, dynamic>.from(item)),
            )
            .toList();
      });
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Failed to load active sessions')),
      );
    } finally {
      if (mounted) setState(() => _loadingSessions = false);
    }
  }

  // A13 — security inbox loader. Pulls the last 20 login_anomalies the
  // server has recorded for this user. Best-effort: a failure just
  // leaves the list empty rather than blocking the rest of the page.
  Future<void> _loadAnomalies() async {
    if (_loadingAnomalies) return;
    setState(() => _loadingAnomalies = true);
    try {
      final response = await ref
          .read(apiClientProvider)
          .get('${Environment.authPath}/security/anomalies');
      final items = (response.data['data'] as List<dynamic>?) ?? [];
      if (!mounted) return;
      setState(() {
        _anomalies = items
            .whereType<Map>()
            .map(
              (item) =>
                  _LoginAnomaly.fromJson(Map<String, dynamic>.from(item)),
            )
            .toList();
      });
    } catch (_) {
      // Soft-fail: empty inbox is the safe default. Don't ruin the
      // page because the inbox didn't load.
    } finally {
      if (mounted) setState(() => _loadingAnomalies = false);
    }
  }

  Future<void> _acknowledgeAnomaly(String anomalyId) async {
    if (_ackingAnomalyIds.contains(anomalyId)) return;
    setState(() => _ackingAnomalyIds.add(anomalyId));
    try {
      await ref
          .read(apiClientProvider)
          .post(
            '${Environment.authPath}/security/anomalies/$anomalyId/ack',
          );
      await _loadAnomalies();
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Failed to dismiss alert')),
      );
    } finally {
      if (mounted) {
        setState(() => _ackingAnomalyIds.remove(anomalyId));
      }
    }
  }

  Future<void> _revokeSession(String sessionId) async {
    try {
      await ref
          .read(apiClientProvider)
          .delete('${Environment.authPath}/sessions/$sessionId');
      await _loadSessions();
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Session revoked')));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Failed to revoke session')));
    }
  }

  Future<void> _logoutEverywhere() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Log out everywhere?', style: AppTextStyles.h3),
        content: Text(
          'This revokes all active sessions, including this device.',
          style: AppTextStyles.body,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Log out'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    try {
      await ref
          .read(apiClientProvider)
          .post('${Environment.authPath}/logout-all');
      ref.read(authServiceProvider).logout();
      if (mounted) context.go('/login');
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Failed to revoke sessions')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new,
            color: AppColors.textPrimary,
          ),
          onPressed: () => context.pop(),
        ),
        title: Text('Security', style: AppTextStyles.h2),
      ),
      body: ListView(
        padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 40),
        children: [
          // --- Change Password Section ---
          _SectionHeader('CHANGE PASSWORD'),
          const SizedBox(height: 12),
          Container(
            padding: const EdgeInsets.all(16),
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Form(
              key: _formKey,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  _PasswordField(
                    controller: _currentPasswordController,
                    label: 'Current Password',
                    obscure: _obscureCurrent,
                    onToggle: () =>
                        setState(() => _obscureCurrent = !_obscureCurrent),
                    validator: (v) => (v == null || v.isEmpty)
                        ? 'Enter your current password'
                        : null,
                  ),
                  const SizedBox(height: 14),
                  _PasswordField(
                    controller: _newPasswordController,
                    label: 'New Password',
                    obscure: _obscureNew,
                    onToggle: () => setState(() => _obscureNew = !_obscureNew),
                    validator: (v) {
                      if (v == null || v.isEmpty) return 'Enter a new password';
                      if (v.length < 8) return 'Must be at least 8 characters';
                      return null;
                    },
                  ),
                  const SizedBox(height: 14),
                  _PasswordField(
                    controller: _confirmPasswordController,
                    label: 'Confirm New Password',
                    obscure: _obscureConfirm,
                    onToggle: () =>
                        setState(() => _obscureConfirm = !_obscureConfirm),
                    validator: (v) {
                      if (v == null || v.isEmpty) {
                        return 'Confirm your new password';
                      }
                      if (v != _newPasswordController.text) {
                        return 'Passwords do not match';
                      }
                      return null;
                    },
                  ),
                  const SizedBox(height: 20),
                  SizedBox(
                    width: double.infinity,
                    child: ElevatedButton(
                      onPressed: _savingPassword ? null : _changePassword,
                      style: ElevatedButton.styleFrom(
                        backgroundColor: AppColors.postbookPrimary,
                        foregroundColor: Colors.white,
                        padding: const EdgeInsets.symmetric(vertical: 14),
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(
                            AppSpacing.radiusLarge,
                          ),
                        ),
                      ),
                      child: _savingPassword
                          ? const SizedBox(
                              height: 20,
                              width: 20,
                              child: CircularProgressIndicator(
                                strokeWidth: 2,
                                color: Colors.white,
                              ),
                            )
                          : Text('Update Password', style: AppTextStyles.label),
                    ),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 24),
          // --- Two-Factor Authentication Section ---
          _SectionHeader('TWO-FACTOR AUTHENTICATION'),
          const SizedBox(height: 12),
          Container(
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: SwitchListTile(
              contentPadding: const EdgeInsets.symmetric(horizontal: 16),
              title: Text(
                'Two-Factor Authentication',
                style: AppTextStyles.body,
              ),
              subtitle: Text(
                _togglingTwoFactor
                    ? 'Updating...'
                    : 'Add an extra layer of security to your account',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textSecondary,
                ),
              ),
              value: _twoFactorEnabled,
              activeThumbColor: AppColors.postbookPrimary,
              onChanged: _togglingTwoFactor ? null : _toggleTwoFactor,
            ),
          ),
          const SizedBox(height: 24),
          // --- Security Alerts (A13) ---
          _SectionHeader('SECURITY ALERTS'),
          const SizedBox(height: 12),
          _buildAnomaliesCard(),
          const SizedBox(height: 24),
          // --- Active Sessions Section ---
          _SectionHeader('ACTIVE SESSIONS'),
          const SizedBox(height: 12),
          _buildSessionsCard(),
        ],
      ),
    );
  }

  Widget _buildAnomaliesCard() {
    final unack = _anomalies.where((a) => a.acknowledgedAt == null).toList();
    final highRisk = unack.where((a) => a.riskScore >= 70).toList();

    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (highRisk.isNotEmpty) ...[
            Container(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: const Color(0xFFFFF1F2),
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                border: Border.all(color: const Color(0xFFFCA5A5)),
              ),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Icon(Icons.warning_amber_rounded,
                      color: Color(0xFFB91C1C), size: 20),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          highRisk.length == 1
                              ? 'We blocked a suspicious sign-in'
                              : '${highRisk.length} suspicious sign-ins blocked',
                          style: AppTextStyles.bodySmall.copyWith(
                            fontWeight: FontWeight.w800,
                            color: const Color(0xFFB91C1C),
                          ),
                        ),
                        const SizedBox(height: 2),
                        Text(
                          "If any of these weren't you, change your password now.",
                          style: AppTextStyles.labelTiny.copyWith(
                            color: const Color(0xFFB91C1C),
                          ),
                        ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
            const SizedBox(height: 12),
          ],
          Row(
            children: [
              const Icon(Icons.shield_outlined,
                  color: AppColors.textMuted, size: 22),
              const SizedBox(width: 12),
              Expanded(
                child: Text(
                  unack.isEmpty
                      ? 'No recent alerts'
                      : '${unack.length} unread alert${unack.length == 1 ? '' : 's'}',
                  style: AppTextStyles.body,
                ),
              ),
              IconButton(
                onPressed: _loadingAnomalies ? null : _loadAnomalies,
                icon: _loadingAnomalies
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.refresh, color: AppColors.textSecondary),
              ),
            ],
          ),
          const SizedBox(height: 10),
          if (_anomalies.isEmpty && !_loadingAnomalies)
            Align(
              alignment: Alignment.centerLeft,
              child: Text(
                "Your account looks healthy. We'll flag any unusual sign-ins here.",
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textSecondary,
                ),
              ),
            )
          else
            ..._anomalies.take(10).map(
                  (a) => _AnomalyTile(
                    anomaly: a,
                    busy: _ackingAnomalyIds.contains(a.id),
                    onAck: () => _acknowledgeAnomaly(a.id),
                  ),
                ),
        ],
      ),
    );
  }

  Widget _buildSessionsCard() {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          Row(
            children: [
              const Icon(
                Icons.devices_outlined,
                color: AppColors.textMuted,
                size: 22,
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Text('Signed-in devices', style: AppTextStyles.body),
              ),
              IconButton(
                onPressed: _loadingSessions ? null : _loadSessions,
                icon: _loadingSessions
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.refresh, color: AppColors.textSecondary),
              ),
            ],
          ),
          const SizedBox(height: 10),
          if (_sessions.isEmpty && !_loadingSessions)
            Align(
              alignment: Alignment.centerLeft,
              child: Text(
                'No active sessions returned by the server.',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textSecondary,
                ),
              ),
            )
          else
            ..._sessions.map(
              (session) => _SessionTile(
                session: session,
                onRevoke: () => _revokeSession(session.id),
              ),
            ),
          const SizedBox(height: 12),
          SizedBox(
            width: double.infinity,
            child: OutlinedButton.icon(
              onPressed: _logoutEverywhere,
              icon: const Icon(Icons.logout),
              label: const Text('Log out everywhere'),
            ),
          ),
        ],
      ),
    );
  }
}

class _ActiveSession {
  const _ActiveSession({
    required this.id,
    required this.platform,
    required this.ip,
    required this.userAgent,
    required this.createdAt,
    required this.expiresAt,
  });

  final String id;
  final String platform;
  final String ip;
  final String userAgent;
  final DateTime? createdAt;
  final DateTime? expiresAt;

  factory _ActiveSession.fromJson(Map<String, dynamic> json) {
    return _ActiveSession(
      id: json['id']?.toString() ?? '',
      platform: json['platform']?.toString() ?? 'Unknown',
      ip: json['ip']?.toString() ?? '',
      userAgent: json['user_agent']?.toString() ?? '',
      createdAt: DateTime.tryParse(json['created_at']?.toString() ?? ''),
      expiresAt: DateTime.tryParse(json['expires_at']?.toString() ?? ''),
    );
  }
}

class _SessionTile extends StatelessWidget {
  const _SessionTile({required this.session, required this.onRevoke});

  final _ActiveSession session;
  final VoidCallback onRevoke;

  @override
  Widget build(BuildContext context) {
    final title = session.platform.isEmpty
        ? 'Unknown device'
        : session.platform;
    final subtitle = [
      if (session.ip.isNotEmpty) session.ip,
      if (session.expiresAt != null)
        'expires ${_dateLabel(session.expiresAt!)}',
    ].join(' - ');

    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: const Icon(Icons.phone_android, color: AppColors.textSecondary),
      title: Text(title, style: AppTextStyles.body),
      subtitle: Text(
        subtitle.isEmpty ? session.userAgent : subtitle,
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
        style: AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
      ),
      trailing: IconButton(
        icon: const Icon(Icons.close, color: AppColors.liveRed),
        onPressed: onRevoke,
      ),
    );
  }

  static String _dateLabel(DateTime value) {
    return '${value.year}-${value.month.toString().padLeft(2, '0')}-${value.day.toString().padLeft(2, '0')}';
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.title);
  final String title;

  @override
  Widget build(BuildContext context) {
    return Text(
      title,
      style: AppTextStyles.labelSmall.copyWith(color: AppColors.textMuted),
    );
  }
}

// A13 — one row in auth.login_anomalies. The server-side schema lives
// in identity-platform/services/auth-service/database/setup.sql; this
// is the wire shape consumed by the security inbox.
class _LoginAnomaly {
  const _LoginAnomaly({
    required this.id,
    required this.anomalyType,
    required this.ip,
    required this.userAgent,
    required this.deviceId,
    required this.countryCode,
    required this.riskScore,
    required this.challenged,
    required this.acknowledgedAt,
    required this.occurredAt,
  });

  final String id;
  final String anomalyType;
  final String ip;
  final String userAgent;
  final String deviceId;
  final String countryCode;
  final int riskScore;
  final bool challenged;
  final DateTime? acknowledgedAt;
  final DateTime? occurredAt;

  factory _LoginAnomaly.fromJson(Map<String, dynamic> json) {
    return _LoginAnomaly(
      id: json['id']?.toString() ?? '',
      anomalyType: json['anomaly_type']?.toString() ?? '',
      ip: json['ip']?.toString() ?? '',
      userAgent: json['user_agent']?.toString() ?? '',
      deviceId: json['device_id']?.toString() ?? '',
      countryCode: json['country_code']?.toString() ?? '',
      riskScore: (json['risk_score'] as num?)?.toInt() ?? 0,
      challenged: json['challenged'] == true,
      acknowledgedAt:
          DateTime.tryParse(json['acknowledged_at']?.toString() ?? ''),
      occurredAt: DateTime.tryParse(json['occurred_at']?.toString() ?? ''),
    );
  }

  String get label {
    switch (anomalyType) {
      case 'new_ip':
        return 'Signed in from a new IP';
      case 'new_device':
        return 'Signed in from a new device';
      case 'new_country':
        return 'Signed in from a new country';
      case 'impossible_travel':
        return 'Impossible-travel alert';
      case 'many_failed':
        return 'Multiple failed sign-in attempts';
      case 'password_reset_used':
        return 'Password reset used';
      case 'session_revoked':
        return 'We blocked a suspicious sign-in';
      default:
        return 'Security alert';
    }
  }
}

class _AnomalyTile extends StatelessWidget {
  const _AnomalyTile({
    required this.anomaly,
    required this.busy,
    required this.onAck,
  });

  final _LoginAnomaly anomaly;
  final bool busy;
  final VoidCallback onAck;

  @override
  Widget build(BuildContext context) {
    final ackd = anomaly.acknowledgedAt != null;
    final high = anomaly.riskScore >= 70;
    final med = !high && anomaly.riskScore >= 30;

    final Color border;
    final Color bg;
    final Color iconColor;
    if (ackd) {
      border = AppColors.borderSubtle;
      bg = AppColors.bgCard;
      iconColor = AppColors.textSecondary;
    } else if (high) {
      border = const Color(0xFFFCA5A5);
      bg = const Color(0xFFFFF1F2);
      iconColor = const Color(0xFFB91C1C);
    } else if (med) {
      border = const Color(0xFFFCD34D);
      bg = const Color(0xFFFFFBEB);
      iconColor = const Color(0xFF92400E);
    } else {
      border = AppColors.borderSubtle;
      bg = AppColors.bgCard;
      iconColor = AppColors.postbookPrimary;
    }

    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: bg,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: border),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(Icons.warning_amber_rounded, color: iconColor, size: 20),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          anomaly.label,
                          style: AppTextStyles.bodySmall.copyWith(
                            fontWeight: FontWeight.w800,
                          ),
                        ),
                      ),
                      Container(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 6, vertical: 2),
                        decoration: BoxDecoration(
                          color: iconColor.withValues(alpha: 0.15),
                          borderRadius: BorderRadius.circular(8),
                        ),
                        child: Text(
                          'Risk ${anomaly.riskScore}',
                          style: AppTextStyles.labelTiny.copyWith(
                            color: iconColor,
                            fontWeight: FontWeight.w800,
                          ),
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 4),
                  Text(
                    [
                      if (anomaly.ip.isNotEmpty) 'IP ${anomaly.ip}',
                      if (anomaly.occurredAt != null)
                        _dateLabelStatic(anomaly.occurredAt!),
                    ].join(' · '),
                    style: AppTextStyles.labelTiny.copyWith(
                      color: AppColors.textSecondary,
                    ),
                  ),
                  if (anomaly.anomalyType == 'session_revoked' && !ackd) ...[
                    const SizedBox(height: 6),
                    Text(
                      "We revoked a sign-in that didn't match this device. "
                      "If it wasn't you, change your password now.",
                      style: AppTextStyles.labelTiny.copyWith(
                        color: const Color(0xFFB91C1C),
                      ),
                    ),
                  ],
                ],
              ),
            ),
            if (!ackd) ...[
              const SizedBox(width: 8),
              TextButton(
                onPressed: busy ? null : onAck,
                style: TextButton.styleFrom(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 8, vertical: 4),
                  minimumSize: const Size(0, 28),
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                child: busy
                    ? const SizedBox(
                        width: 14,
                        height: 14,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : Text(
                        'Dismiss',
                        style: AppTextStyles.labelTiny.copyWith(
                          color: AppColors.postbookPrimary,
                          fontWeight: FontWeight.w800,
                        ),
                      ),
              ),
            ],
          ],
        ),
      ),
    );
  }

  // Local copy of the date formatter from _SessionTile — kept private
  // so a future refactor can pull both into a shared helper without
  // touching either widget's API.
  static String _dateLabelStatic(DateTime value) {
    final now = DateTime.now();
    final diff = now.difference(value);
    if (diff.inMinutes < 1) return 'just now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    if (diff.inHours < 24) return '${diff.inHours}h ago';
    if (diff.inDays < 7) return '${diff.inDays}d ago';
    return '${value.year}-${value.month.toString().padLeft(2, '0')}-${value.day.toString().padLeft(2, '0')}';
  }
}

class _PasswordField extends StatelessWidget {
  const _PasswordField({
    required this.controller,
    required this.label,
    required this.obscure,
    required this.onToggle,
    this.validator,
  });

  final TextEditingController controller;
  final String label;
  final bool obscure;
  final VoidCallback onToggle;
  final String? Function(String?)? validator;

  @override
  Widget build(BuildContext context) {
    return TextFormField(
      controller: controller,
      obscureText: obscure,
      validator: validator,
      style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
      decoration: InputDecoration(
        labelText: label,
        labelStyle: AppTextStyles.bodySmall.copyWith(
          color: AppColors.textSecondary,
        ),
        filled: true,
        fillColor: AppColors.bgSecondary,
        contentPadding: const EdgeInsets.symmetric(
          horizontal: 16,
          vertical: 12,
        ),
        suffixIcon: IconButton(
          icon: Icon(
            obscure ? Icons.visibility_outlined : Icons.visibility_off_outlined,
            color: AppColors.textMuted,
            size: 20,
          ),
          onPressed: onToggle,
        ),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          borderSide: BorderSide(color: AppColors.borderSubtle),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          borderSide: BorderSide(color: AppColors.borderSubtle),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          borderSide: BorderSide(color: AppColors.postbookPrimary, width: 1.5),
        ),
        errorBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          borderSide: const BorderSide(color: Colors.red),
        ),
        focusedErrorBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          borderSide: const BorderSide(color: Colors.red, width: 1.5),
        ),
      ),
    );
  }
}
