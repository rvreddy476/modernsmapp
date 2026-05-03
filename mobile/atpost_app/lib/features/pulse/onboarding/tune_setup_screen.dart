import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/l10n/app_strings.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Sprint 1 — Pulse onboarding step B2: Tune setup.
///
/// Five required axes (lifestyle rhythm, conversation style, relationship
/// intent, faith & family weight, languages) plus two marriage-only axes
/// (family plans, education importance). Spec: PULSE_DATING_SPEC.md §6.1.6.
///
/// On save: PUT `/v1/dating/tune` and forward to the Echoes consent screen.
class TuneSetupScreen extends ConsumerStatefulWidget {
  const TuneSetupScreen({super.key, this.initialIntent});

  /// Pre-fills the relationship intent axis. Usually passed from the intent
  /// picker (B1); falls back to "serious" if absent.
  final String? initialIntent;

  @override
  ConsumerState<TuneSetupScreen> createState() => _TuneSetupScreenState();
}

class _TuneSetupScreenState extends ConsumerState<TuneSetupScreen> {
  static const _conversationOptions = <String>[
    'witty',
    'deep',
    'playful',
    'direct',
    'reflective',
  ];

  static const _languageOptions = <_LanguageOption>[
    _LanguageOption(code: 'en', labelKey: 'pulse.tune.languages.english'),
    _LanguageOption(code: 'hi', labelKey: 'pulse.tune.languages.hindi'),
    _LanguageOption(code: 'ta', labelKey: 'pulse.tune.languages.tamil'),
    _LanguageOption(code: 'te', labelKey: 'pulse.tune.languages.telugu'),
    _LanguageOption(code: 'bn', labelKey: 'pulse.tune.languages.bengali'),
    _LanguageOption(code: 'mr', labelKey: 'pulse.tune.languages.marathi'),
  ];

  double _lifestyle = 3;
  String? _conversation;
  late String _intent;
  double _faithFamily = 3;
  final Set<String> _languages = {'en'};
  double _familyPlans = 3;
  double _education = 3;

  bool _saving = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _intent = widget.initialIntent ?? 'serious';
  }

  bool get _isMarriageIntent => _intent == 'marriage';

  /// Count axes the user has actually engaged with. Required for "Skip for
  /// now" — needs at least 3 axes set to neutral defaults to proceed.
  int get _axesEngaged {
    var count = 0;
    // Sliders are always engaged (they have a default), so they always count.
    count += 1; // lifestyle
    count += 1; // faith
    if (_conversation != null) count += 1;
    if (_languages.isNotEmpty) count += 1;
    count += 1; // intent (always set)
    return count;
  }

  Map<String, dynamic> _buildPayload() {
    return <String, dynamic>{
      'lifestyle_rhythm': _lifestyle.round(),
      if (_conversation != null) 'conversation_style': _conversation,
      'relationship_intent': _intent,
      'faith_family_weight': _faithFamily.round(),
      'languages': _languages.toList()..sort(),
      if (_isMarriageIntent) 'family_plans': _familyPlans.round(),
      if (_isMarriageIntent) 'education_importance': _education.round(),
    };
  }

  Future<void> _save({required bool skipping}) async {
    if (_saving) return;
    setState(() {
      _saving = true;
      _error = null;
    });
    final payload = _buildPayload();
    if (skipping) {
      // Ensure 3 axes have neutral defaults: sliders + a default conversation.
      payload['conversation_style'] ??= 'reflective';
      if (!payload.containsKey('languages') ||
          (payload['languages'] as List).isEmpty) {
        payload['languages'] = <String>['en'];
      }
    }
    try {
      await ref.read(pulseRepositoryProvider).updateTune(payload);
      if (!mounted) return;
      context.go('/pulse/onboarding/echoes');
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _error = AppStrings.t('pulse.tune.error.save');
      });
    } finally {
      if (mounted) {
        setState(() => _saving = false);
      }
    }
  }

  bool get _canSave =>
      _conversation != null && _languages.isNotEmpty && !_saving;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        title: Text(AppStrings.t('pulse.app.name'), style: AppTextStyles.h2),
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        actions: [
          TextButton(
            onPressed: _saving || _axesEngaged < 3
                ? null
                : () => _save(skipping: true),
            child: Text(
              AppStrings.t('pulse.tune.skip'),
              style: AppTextStyles.bodyMedium,
            ),
          ),
        ],
      ),
      body: SafeArea(
        child: Padding(
          padding: AppSpacing.pagePadding,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const SizedBox(height: AppSpacing.l),
              Text(
                AppStrings.t('pulse.tune.title'),
                style: AppTextStyles.h1,
              ),
              const SizedBox(height: AppSpacing.m),
              Text(
                AppStrings.t('pulse.tune.subtitle'),
                style: AppTextStyles.body,
              ),
              const SizedBox(height: AppSpacing.xxl),
              Expanded(
                child: ListView(
                  children: [
                    _AxisSlider(
                      labelKey: 'pulse.tune.lifestyle.label',
                      hintKey: 'pulse.tune.lifestyle.hint',
                      leftKey: 'pulse.tune.lifestyle.left',
                      rightKey: 'pulse.tune.lifestyle.right',
                      value: _lifestyle,
                      onChanged: (v) => setState(() => _lifestyle = v),
                    ),
                    const SizedBox(height: AppSpacing.xxl),
                    _ConversationStyleAxis(
                      selected: _conversation,
                      options: _conversationOptions,
                      onSelected: (v) => setState(() => _conversation = v),
                    ),
                    const SizedBox(height: AppSpacing.xxl),
                    _IntentAxis(
                      selected: _intent,
                      onChanged: (v) => setState(() => _intent = v),
                    ),
                    const SizedBox(height: AppSpacing.xxl),
                    _AxisSlider(
                      labelKey: 'pulse.tune.faith.label',
                      hintKey: 'pulse.tune.faith.hint',
                      leftKey: 'pulse.tune.faith.left',
                      rightKey: 'pulse.tune.faith.right',
                      value: _faithFamily,
                      onChanged: (v) => setState(() => _faithFamily = v),
                    ),
                    const SizedBox(height: AppSpacing.xxl),
                    _LanguagesAxis(
                      options: _languageOptions,
                      selected: _languages,
                      onToggle: (code) {
                        setState(() {
                          if (_languages.contains(code)) {
                            if (_languages.length > 1) {
                              _languages.remove(code);
                            }
                          } else {
                            _languages.add(code);
                          }
                        });
                      },
                    ),
                    if (_isMarriageIntent) ...[
                      const SizedBox(height: AppSpacing.xxl),
                      _AxisSlider(
                        labelKey: 'pulse.tune.familyPlans.label',
                        hintKey: 'pulse.tune.familyPlans.hint',
                        leftKey: 'pulse.tune.familyPlans.left',
                        rightKey: 'pulse.tune.familyPlans.right',
                        value: _familyPlans,
                        onChanged: (v) => setState(() => _familyPlans = v),
                      ),
                      const SizedBox(height: AppSpacing.xxl),
                      _AxisSlider(
                        labelKey: 'pulse.tune.education.label',
                        hintKey: 'pulse.tune.education.hint',
                        leftKey: 'pulse.tune.education.left',
                        rightKey: 'pulse.tune.education.right',
                        value: _education,
                        onChanged: (v) => setState(() => _education = v),
                      ),
                    ],
                    const SizedBox(height: AppSpacing.xxxl),
                  ],
                ),
              ),
              if (_error != null) ...[
                const SizedBox(height: AppSpacing.m),
                Text(
                  _error!,
                  style: AppTextStyles.body.copyWith(
                    color: AppColors.statusError,
                  ),
                ),
              ],
              const SizedBox(height: AppSpacing.l),
              SizedBox(
                width: double.infinity,
                child: FilledButton(
                  onPressed: _canSave ? () => _save(skipping: false) : null,
                  style: FilledButton.styleFrom(
                    backgroundColor: AppColors.postgramPrimary,
                    padding: const EdgeInsets.symmetric(vertical: 14),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(
                        AppSpacing.radiusMedium,
                      ),
                    ),
                  ),
                  child: _saving
                      ? const SizedBox(
                          height: 18,
                          width: 18,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            color: Colors.white,
                          ),
                        )
                      : Text(
                          AppStrings.t('pulse.tune.save'),
                          style: AppTextStyles.bodyMedium.copyWith(
                            color: Colors.white,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                ),
              ),
              const SizedBox(height: AppSpacing.l),
            ],
          ),
        ),
      ),
    );
  }
}

class _LanguageOption {
  const _LanguageOption({required this.code, required this.labelKey});
  final String code;
  final String labelKey;
}

class _AxisSlider extends StatelessWidget {
  const _AxisSlider({
    required this.labelKey,
    required this.hintKey,
    required this.leftKey,
    required this.rightKey,
    required this.value,
    required this.onChanged,
  });

  final String labelKey;
  final String hintKey;
  final String leftKey;
  final String rightKey;
  final double value;
  final ValueChanged<double> onChanged;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(AppStrings.t(labelKey), style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.xs),
        Text(AppStrings.t(hintKey), style: AppTextStyles.bodySmall),
        const SizedBox(height: AppSpacing.s),
        Slider(
          value: value,
          min: 1,
          max: 5,
          divisions: 4,
          activeColor: AppColors.postgramPrimary,
          inactiveColor: AppColors.borderMedium,
          label: value.round().toString(),
          onChanged: onChanged,
        ),
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            Text(AppStrings.t(leftKey), style: AppTextStyles.labelSmall),
            Text(AppStrings.t(rightKey), style: AppTextStyles.labelSmall),
          ],
        ),
      ],
    );
  }
}

class _ConversationStyleAxis extends StatelessWidget {
  const _ConversationStyleAxis({
    required this.selected,
    required this.options,
    required this.onSelected,
  });

  final String? selected;
  final List<String> options;
  final ValueChanged<String> onSelected;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          AppStrings.t('pulse.tune.conversation.label'),
          style: AppTextStyles.h3,
        ),
        const SizedBox(height: AppSpacing.xs),
        Text(
          AppStrings.t('pulse.tune.conversation.hint'),
          style: AppTextStyles.bodySmall,
        ),
        const SizedBox(height: AppSpacing.m),
        Wrap(
          spacing: AppSpacing.m,
          runSpacing: AppSpacing.m,
          children: options.map((value) {
            final isSelected = value == selected;
            return InkWell(
              onTap: () => onSelected(value),
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              child: Container(
                padding: const EdgeInsets.symmetric(
                  horizontal: 14,
                  vertical: 10,
                ),
                decoration: BoxDecoration(
                  color: isSelected
                      ? AppColors.postgramPrimary.withValues(alpha: 0.18)
                      : AppColors.bgCard,
                  borderRadius: BorderRadius.circular(
                    AppSpacing.radiusMedium,
                  ),
                  border: Border.all(
                    color: isSelected
                        ? AppColors.postgramPrimary
                        : AppColors.borderSubtle,
                    width: isSelected ? 2 : 1,
                  ),
                ),
                child: Text(
                  AppStrings.t('pulse.tune.conversation.$value'),
                  style: AppTextStyles.bodyMedium.copyWith(
                    color: isSelected
                        ? AppColors.postgramPrimary
                        : AppColors.textSecondary,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            );
          }).toList(),
        ),
      ],
    );
  }
}

class _IntentAxis extends StatelessWidget {
  const _IntentAxis({required this.selected, required this.onChanged});

  final String selected;
  final ValueChanged<String> onChanged;

  static const _options = [
    ('casual', 'pulse.intent.casual.title'),
    ('serious', 'pulse.intent.serious.title'),
    ('marriage', 'pulse.intent.marriage.title'),
  ];

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          AppStrings.t('pulse.tune.intent.label'),
          style: AppTextStyles.h3,
        ),
        const SizedBox(height: AppSpacing.xs),
        Text(
          AppStrings.t('pulse.tune.intent.hint'),
          style: AppTextStyles.bodySmall,
        ),
        const SizedBox(height: AppSpacing.m),
        SegmentedButton<String>(
          segments: _options
              .map(
                (option) => ButtonSegment<String>(
                  value: option.$1,
                  label: Text(AppStrings.t(option.$2)),
                ),
              )
              .toList(),
          selected: {selected},
          onSelectionChanged: (set) {
            if (set.isNotEmpty) onChanged(set.first);
          },
        ),
      ],
    );
  }
}

class _LanguagesAxis extends StatelessWidget {
  const _LanguagesAxis({
    required this.options,
    required this.selected,
    required this.onToggle,
  });

  final List<_LanguageOption> options;
  final Set<String> selected;
  final ValueChanged<String> onToggle;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          AppStrings.t('pulse.tune.languages.label'),
          style: AppTextStyles.h3,
        ),
        const SizedBox(height: AppSpacing.xs),
        Text(
          AppStrings.t('pulse.tune.languages.hint'),
          style: AppTextStyles.bodySmall,
        ),
        const SizedBox(height: AppSpacing.m),
        Wrap(
          spacing: AppSpacing.m,
          runSpacing: AppSpacing.m,
          children: options.map((option) {
            final isSelected = selected.contains(option.code);
            return FilterChip(
              label: Text(AppStrings.t(option.labelKey)),
              selected: isSelected,
              onSelected: (_) => onToggle(option.code),
              backgroundColor: AppColors.bgCard,
              selectedColor:
                  AppColors.postgramPrimary.withValues(alpha: 0.18),
              checkmarkColor: AppColors.postgramPrimary,
              side: BorderSide(
                color: isSelected
                    ? AppColors.postgramPrimary
                    : AppColors.borderSubtle,
              ),
            );
          }).toList(),
        ),
      ],
    );
  }
}
