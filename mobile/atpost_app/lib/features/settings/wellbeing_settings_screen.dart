import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

const _kBrandRed = Color(0xFFD8103F);

class WellbeingSettingsScreen extends ConsumerStatefulWidget {
  const WellbeingSettingsScreen({super.key});

  @override
  ConsumerState<WellbeingSettingsScreen> createState() =>
      _WellbeingSettingsScreenState();
}

class _WellbeingSettingsScreenState
    extends ConsumerState<WellbeingSettingsScreen> {
  // Screen time
  bool _screenTimeLoading = true;
  bool _screenTimeError = false;
  List<Map<String, dynamic>> _screenTimeData = [];

  // Daily Limit
  bool _dailyLimitEnabled = false;
  final _dailyLimitController = TextEditingController();
  bool _savingDailyLimit = false;

  // Focus Mode
  bool _focusModeEnabled = false;
  final _focusStartController = TextEditingController(text: '09:00');
  final _focusEndController = TextEditingController(text: '17:00');
  bool _savingFocusMode = false;

  // Bedtime Mode
  bool _bedtimeEnabled = false;
  final _bedtimeStartController = TextEditingController(text: '22:00');
  final _bedtimeEndController = TextEditingController(text: '07:00');
  bool _savingBedtime = false;

  // Break Reminders
  bool _breakRemindersEnabled = false;
  int _breakIntervalMinutes = 60;

  // Content Preferences
  bool _hideLikeCounts = false;
  bool _hideViewCounts = false;

  @override
  void initState() {
    super.initState();
    _loadScreenTime();
  }

  @override
  void dispose() {
    _dailyLimitController.dispose();
    _focusStartController.dispose();
    _focusEndController.dispose();
    _bedtimeStartController.dispose();
    _bedtimeEndController.dispose();
    super.dispose();
  }

  Future<void> _loadScreenTime() async {
    setState(() {
      _screenTimeLoading = true;
      _screenTimeError = false;
    });
    try {
      final res =
          await ref.read(apiClientProvider).get('/v1/users/me/screen-time');
      final raw = res.data['data'];
      if (raw is List && mounted) {
        setState(() {
          _screenTimeData = raw.cast<Map<String, dynamic>>();
        });
      } else if (mounted) {
        setState(() {
          _screenTimeData = _defaultWeekData();
        });
      }
    } catch (_) {
      if (mounted) {
        setState(() {
          _screenTimeError = true;
          _screenTimeData = _defaultWeekData();
        });
      }
    } finally {
      if (mounted) setState(() => _screenTimeLoading = false);
    }
  }

  List<Map<String, dynamic>> _defaultWeekData() {
    const days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
    return days
        .map((d) => <String, dynamic>{'day': d, 'minutes_active': 0})
        .toList();
  }

  Future<void> _saveSection(Map<String, dynamic> data) async {
    await ref.read(apiClientProvider).put(
          '/v1/users/me/wellbeing',
          data: data,
        );
  }

  void _showSaved() {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Saved')),
    );
  }

  void _showError(String msg) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(msg)),
    );
  }

  Future<void> _saveDailyLimit() async {
    setState(() => _savingDailyLimit = true);
    try {
      await _saveSection({
        'daily_limit_enabled': _dailyLimitEnabled,
        'daily_limit_minutes': int.tryParse(_dailyLimitController.text) ?? 120,
      });
      _showSaved();
    } catch (_) {
      _showError('Failed to save daily limit');
    } finally {
      if (mounted) setState(() => _savingDailyLimit = false);
    }
  }

  Future<void> _saveFocusMode() async {
    setState(() => _savingFocusMode = true);
    try {
      await _saveSection({
        'focus_mode_enabled': _focusModeEnabled,
        'focus_start': _focusStartController.text,
        'focus_end': _focusEndController.text,
      });
      _showSaved();
    } catch (_) {
      _showError('Failed to save focus mode');
    } finally {
      if (mounted) setState(() => _savingFocusMode = false);
    }
  }

  Future<void> _saveBedtime() async {
    setState(() => _savingBedtime = true);
    try {
      await _saveSection({
        'bedtime_enabled': _bedtimeEnabled,
        'bedtime_start': _bedtimeStartController.text,
        'bedtime_end': _bedtimeEndController.text,
      });
      _showSaved();
    } catch (_) {
      _showError('Failed to save bedtime mode');
    } finally {
      if (mounted) setState(() => _savingBedtime = false);
    }
  }

  Future<void> _saveBreakReminders(bool value) async {
    setState(() => _breakRemindersEnabled = value);
    try {
      await _saveSection({
        'break_reminders_enabled': value,
        'break_interval_minutes': _breakIntervalMinutes,
      });
    } catch (_) {
      if (mounted) setState(() => _breakRemindersEnabled = !value);
      _showError('Failed to update break reminders');
    }
  }

  Future<void> _saveBreakInterval(int? value) async {
    if (value == null) return;
    setState(() => _breakIntervalMinutes = value);
    try {
      await _saveSection({
        'break_reminders_enabled': _breakRemindersEnabled,
        'break_interval_minutes': value,
      });
    } catch (_) {
      _showError('Failed to update interval');
    }
  }

  Future<void> _saveContentPref(String key, bool value) async {
    if (key == 'hide_like_counts') {
      setState(() => _hideLikeCounts = value);
    } else {
      setState(() => _hideViewCounts = value);
    }
    try {
      await _saveSection({
        'hide_like_counts': _hideLikeCounts,
        'hide_view_counts': _hideViewCounts,
      });
    } catch (_) {
      if (key == 'hide_like_counts') {
        if (mounted) setState(() => _hideLikeCounts = !value);
      } else {
        if (mounted) setState(() => _hideViewCounts = !value);
      }
      _showError('Failed to save preference');
    }
  }

  int get _totalMinutes =>
      _screenTimeData.fold(0, (sum, d) => sum + ((d['minutes_active'] as num?)?.toInt() ?? 0));

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
        title: Text('Digital Wellbeing', style: AppTextStyles.h2),
      ),
      body: SingleChildScrollView(
        padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 40),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            _buildScreenTimeSection(),
            const SizedBox(height: 24),
            _buildDailyLimitSection(),
            const SizedBox(height: 24),
            _buildFocusModeSection(),
            const SizedBox(height: 24),
            _buildBedtimeModeSection(),
            const SizedBox(height: 24),
            _buildBreakRemindersSection(),
            const SizedBox(height: 24),
            _buildContentPreferencesSection(),
          ],
        ),
      ),
    );
  }

  // ---------------------------------------------------------------------------
  // Screen Time
  // ---------------------------------------------------------------------------
  Widget _buildScreenTimeSection() {
    return _WellbeingSection(
      title: 'SCREEN TIME',
      child: _screenTimeLoading
          ? const Center(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: CircularProgressIndicator(color: _kBrandRed),
              ),
            )
          : Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (_screenTimeError)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 8),
                    child: Text(
                      'Could not load screen time. Showing cached data.',
                      style: AppTextStyles.bodySmall
                          .copyWith(color: AppColors.textMuted),
                    ),
                  ),
                _BarChart(data: _screenTimeData),
                const SizedBox(height: 12),
                Text(
                  'Total this week: $_totalMinutes mins',
                  style: AppTextStyles.label
                      .copyWith(color: AppColors.textSecondary),
                ),
                if (_screenTimeError) ...[
                  const SizedBox(height: 12),
                  TextButton.icon(
                    onPressed: _loadScreenTime,
                    icon: const Icon(Icons.refresh, size: 16, color: _kBrandRed),
                    label: Text(
                      'Retry',
                      style: AppTextStyles.label.copyWith(color: _kBrandRed),
                    ),
                  ),
                ],
              ],
            ),
    );
  }

  // ---------------------------------------------------------------------------
  // Daily Limit
  // ---------------------------------------------------------------------------
  Widget _buildDailyLimitSection() {
    return _WellbeingSection(
      title: 'DAILY LIMIT',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SwitchListTile(
            contentPadding: EdgeInsets.zero,
            title: Text('Enable Daily Limit', style: AppTextStyles.body),
            subtitle: Text(
              'Stop notifications when daily usage cap is reached',
              style:
                  AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
            ),
            value: _dailyLimitEnabled,
            activeThumbColor: _kBrandRed,
            onChanged: (v) => setState(() => _dailyLimitEnabled = v),
          ),
          if (_dailyLimitEnabled) ...[
            const SizedBox(height: 8),
            TextField(
              controller: _dailyLimitController,
              keyboardType: TextInputType.number,
              style:
                  AppTextStyles.body.copyWith(color: AppColors.textPrimary),
              decoration: _inputDecoration('Daily limit (minutes)'),
            ),
            const SizedBox(height: 12),
          ],
          _SaveButton(
            loading: _savingDailyLimit,
            onPressed: _saveDailyLimit,
          ),
        ],
      ),
    );
  }

  // ---------------------------------------------------------------------------
  // Focus Mode
  // ---------------------------------------------------------------------------
  Widget _buildFocusModeSection() {
    return _WellbeingSection(
      title: 'FOCUS MODE',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SwitchListTile(
            contentPadding: EdgeInsets.zero,
            title: Text('Enable Focus Mode', style: AppTextStyles.body),
            subtitle: Text(
              'Minimise distractions during your focus hours',
              style:
                  AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
            ),
            value: _focusModeEnabled,
            activeThumbColor: _kBrandRed,
            onChanged: (v) => setState(() => _focusModeEnabled = v),
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: TextFormField(
                  controller: _focusStartController,
                  style: AppTextStyles.body
                      .copyWith(color: AppColors.textPrimary),
                  decoration: _inputDecoration('Start time', hint: '09:00'),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: TextFormField(
                  controller: _focusEndController,
                  style: AppTextStyles.body
                      .copyWith(color: AppColors.textPrimary),
                  decoration: _inputDecoration('End time', hint: '17:00'),
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          _SaveButton(
            loading: _savingFocusMode,
            onPressed: _saveFocusMode,
          ),
        ],
      ),
    );
  }

  // ---------------------------------------------------------------------------
  // Bedtime Mode
  // ---------------------------------------------------------------------------
  Widget _buildBedtimeModeSection() {
    return _WellbeingSection(
      title: 'BEDTIME MODE',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SwitchListTile(
            contentPadding: EdgeInsets.zero,
            title: Text('Enable Bedtime Mode', style: AppTextStyles.body),
            subtitle: Text(
              'Silence notifications and dim the screen at bedtime',
              style:
                  AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
            ),
            value: _bedtimeEnabled,
            activeThumbColor: _kBrandRed,
            onChanged: (v) => setState(() => _bedtimeEnabled = v),
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: TextFormField(
                  controller: _bedtimeStartController,
                  style: AppTextStyles.body
                      .copyWith(color: AppColors.textPrimary),
                  decoration: _inputDecoration('Start time', hint: '22:00'),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: TextFormField(
                  controller: _bedtimeEndController,
                  style: AppTextStyles.body
                      .copyWith(color: AppColors.textPrimary),
                  decoration: _inputDecoration('End time', hint: '07:00'),
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          _SaveButton(
            loading: _savingBedtime,
            onPressed: _saveBedtime,
          ),
        ],
      ),
    );
  }

  // ---------------------------------------------------------------------------
  // Break Reminders
  // ---------------------------------------------------------------------------
  Widget _buildBreakRemindersSection() {
    return _WellbeingSection(
      title: 'BREAK REMINDERS',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SwitchListTile(
            contentPadding: EdgeInsets.zero,
            title: Text('Enable Break Reminders', style: AppTextStyles.body),
            subtitle: Text(
              'Get nudged to take a short break during long sessions',
              style:
                  AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
            ),
            value: _breakRemindersEnabled,
            activeThumbColor: _kBrandRed,
            onChanged: _saveBreakReminders,
          ),
          if (_breakRemindersEnabled) ...[
            const SizedBox(height: 8),
            Row(
              children: [
                Text('Remind me:', style: AppTextStyles.body),
                const SizedBox(width: 12),
                DropdownButton<int>(
                  value: _breakIntervalMinutes,
                  dropdownColor: AppColors.bgCard,
                  style: AppTextStyles.body
                      .copyWith(color: AppColors.textPrimary),
                  items: const [30, 60, 90, 120]
                      .map(
                        (m) => DropdownMenuItem(
                          value: m,
                          child: Text('Every $m minutes'),
                        ),
                      )
                      .toList(),
                  onChanged: _saveBreakInterval,
                ),
              ],
            ),
          ],
        ],
      ),
    );
  }

  // ---------------------------------------------------------------------------
  // Content Preferences
  // ---------------------------------------------------------------------------
  Widget _buildContentPreferencesSection() {
    return _WellbeingSection(
      title: 'CONTENT PREFERENCES',
      child: Column(
        children: [
          SwitchListTile(
            contentPadding: EdgeInsets.zero,
            title: Text('Hide like counts', style: AppTextStyles.body),
            subtitle: Text(
              'Stop seeing how many likes posts have received',
              style:
                  AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
            ),
            value: _hideLikeCounts,
            activeThumbColor: _kBrandRed,
            onChanged: (v) => _saveContentPref('hide_like_counts', v),
          ),
          SwitchListTile(
            contentPadding: EdgeInsets.zero,
            title: Text('Hide view counts', style: AppTextStyles.body),
            subtitle: Text(
              'Stop seeing how many times videos have been viewed',
              style:
                  AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
            ),
            value: _hideViewCounts,
            activeThumbColor: _kBrandRed,
            onChanged: (v) => _saveContentPref('hide_view_counts', v),
          ),
        ],
      ),
    );
  }

  InputDecoration _inputDecoration(String label, {String? hint}) {
    return InputDecoration(
      labelText: label,
      hintText: hint,
      labelStyle:
          AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
      hintStyle: AppTextStyles.bodySmall.copyWith(color: AppColors.textMuted),
      filled: true,
      fillColor: AppColors.bgSecondary,
      contentPadding:
          const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        borderSide: const BorderSide(color: AppColors.borderSubtle),
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        borderSide: const BorderSide(color: AppColors.borderSubtle),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        borderSide: const BorderSide(color: _kBrandRed, width: 1.5),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Shared sub-widgets
// ---------------------------------------------------------------------------

class _WellbeingSection extends StatelessWidget {
  const _WellbeingSection({required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          title,
          style: AppTextStyles.labelSmall.copyWith(color: AppColors.textMuted),
        ),
        const SizedBox(height: 10),
        Container(
          width: double.infinity,
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: child,
        ),
      ],
    );
  }
}

class _SaveButton extends StatelessWidget {
  const _SaveButton({required this.loading, required this.onPressed});

  final bool loading;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: double.infinity,
      child: ElevatedButton(
        onPressed: loading ? null : onPressed,
        style: ElevatedButton.styleFrom(
          backgroundColor: const Color(0xFFD8103F),
          foregroundColor: Colors.white,
          padding: const EdgeInsets.symmetric(vertical: 14),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          ),
        ),
        child: loading
            ? const SizedBox(
                height: 20,
                width: 20,
                child: CircularProgressIndicator(
                    strokeWidth: 2, color: Colors.white),
              )
            : Text('Save', style: AppTextStyles.label),
      ),
    );
  }
}

class _BarChart extends StatelessWidget {
  const _BarChart({required this.data});

  final List<Map<String, dynamic>> data;

  @override
  Widget build(BuildContext context) {
    if (data.isEmpty) {
      return Text(
        'No screen time data available.',
        style: AppTextStyles.bodySmall.copyWith(color: AppColors.textMuted),
      );
    }
    return SizedBox(
      height: 100,
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        mainAxisAlignment: MainAxisAlignment.spaceAround,
        children: data.map((d) {
          final minutes = (d['minutes_active'] as num?)?.toDouble() ?? 0.0;
          final barH = minutes.clamp(4.0, 80.0);
          final label = d['day'] as String? ?? '';
          return Column(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              Text(
                minutes.toInt().toString(),
                style: AppTextStyles.labelSmall
                    .copyWith(color: AppColors.textMuted, fontSize: 9),
              ),
              const SizedBox(height: 4),
              Container(
                width: 24,
                height: barH,
                decoration: BoxDecoration(
                  color: const Color(0xFFD8103F),
                  borderRadius: BorderRadius.circular(4),
                ),
              ),
              const SizedBox(height: 4),
              Text(
                label,
                style: AppTextStyles.labelSmall
                    .copyWith(color: AppColors.textSecondary, fontSize: 10),
              ),
            ],
          );
        }).toList(),
      ),
    );
  }
}
