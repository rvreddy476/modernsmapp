import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/data/repositories/qa_repository.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Bottom sheet that lets community admins edit Q&A settings.
Future<void> showCommunityQaSettingsSheet(
  BuildContext context,
  String communityId,
) {
  return showModalBottomSheet<void>(
    context: context,
    backgroundColor: AppColors.bgCard,
    isScrollControlled: true,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
    ),
    builder: (ctx) => Padding(
      padding: EdgeInsets.only(
        bottom: MediaQuery.of(ctx).viewInsets.bottom,
      ),
      child: _CommunityQaSettingsSheet(communityId: communityId),
    ),
  );
}

class _CommunityQaSettingsSheet extends ConsumerStatefulWidget {
  final String communityId;
  const _CommunityQaSettingsSheet({required this.communityId});

  @override
  ConsumerState<_CommunityQaSettingsSheet> createState() =>
      _CommunityQaSettingsSheetState();
}

class _CommunityQaSettingsSheetState
    extends ConsumerState<_CommunityQaSettingsSheet> {
  CommunityQaSettings? _settings;
  late final TextEditingController _welcomeController;
  bool _saving = false;
  bool _initialized = false;

  static const _permissionOptions = ['everyone', 'members', 'moderators'];

  @override
  void initState() {
    super.initState();
    _welcomeController = TextEditingController();
  }

  @override
  void dispose() {
    _welcomeController.dispose();
    super.dispose();
  }

  void _hydrate(CommunityQaSettings s) {
    if (_initialized) return;
    _initialized = true;
    _welcomeController.text = s.welcomeMessage;
    _settings = s;
  }

  Future<void> _save() async {
    if (_settings == null || _saving) return;
    setState(() => _saving = true);
    try {
      final updated = _settings!.copyWith(
        welcomeMessage: _welcomeController.text.trim(),
      );
      await ref
          .read(qaRepositoryProvider)
          .updateCommunityQASettings(widget.communityId, updated);
      ref.invalidate(qaCommunitySettingsProvider(widget.communityId));
      if (!mounted) return;
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Q&A settings saved.')),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not save settings.')),
      );
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final settingsAsync =
        ref.watch(qaCommunitySettingsProvider(widget.communityId));

    return settingsAsync.when(
      loading: () => const SizedBox(
        height: 200,
        child: Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
      ),
      error: (_, _) => Padding(
        padding: const EdgeInsets.all(20),
        child: Text(
          'Could not load settings.',
          style: AppTextStyles.body.copyWith(color: AppColors.textDim),
        ),
      ),
      data: (loaded) {
        _hydrate(loaded);
        final s = _settings!;
        return SingleChildScrollView(
          padding: const EdgeInsets.all(20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Q&A settings', style: AppTextStyles.h2),
              const SizedBox(height: 12),
              SwitchListTile.adaptive(
                contentPadding: EdgeInsets.zero,
                value: s.qaEnabled,
                onChanged: (val) =>
                    setState(() => _settings = s.copyWith(qaEnabled: val)),
                title: Text('Enable Q&A', style: AppTextStyles.body),
              ),
              _PermissionRow(
                label: 'Who can ask',
                value: s.askPermission,
                options: _permissionOptions,
                onChanged: (val) => setState(
                    () => _settings = s.copyWith(askPermission: val)),
              ),
              _PermissionRow(
                label: 'Who can answer',
                value: s.answerPermission,
                options: _permissionOptions,
                onChanged: (val) => setState(
                    () => _settings = s.copyWith(answerPermission: val)),
              ),
              SwitchListTile.adaptive(
                contentPadding: EdgeInsets.zero,
                value: s.autoSuggestTopics,
                onChanged: (val) => setState(() =>
                    _settings = s.copyWith(autoSuggestTopics: val)),
                title:
                    Text('Auto-suggest topics', style: AppTextStyles.body),
              ),
              SwitchListTile.adaptive(
                contentPadding: EdgeInsets.zero,
                value: s.requireApproval,
                onChanged: (val) => setState(
                    () => _settings = s.copyWith(requireApproval: val)),
                title: Text('Require approval before publish',
                    style: AppTextStyles.body),
              ),
              SwitchListTile.adaptive(
                contentPadding: EdgeInsets.zero,
                value: s.anonymityEnabled,
                onChanged: (val) => setState(
                    () => _settings = s.copyWith(anonymityEnabled: val)),
                title: Text('Allow anonymous posts',
                    style: AppTextStyles.body),
              ),
              const SizedBox(height: 8),
              TextField(
                controller: _welcomeController,
                minLines: 2,
                maxLines: 4,
                decoration: const InputDecoration(
                  labelText: 'Welcome message',
                  border: OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 16),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  TextButton(
                    onPressed:
                        _saving ? null : () => Navigator.of(context).pop(),
                    child: const Text('Cancel'),
                  ),
                  const SizedBox(width: 8),
                  ElevatedButton(
                    style: ElevatedButton.styleFrom(
                      backgroundColor: AppColors.postbookPrimary,
                      foregroundColor: Colors.white,
                    ),
                    onPressed: _saving ? null : _save,
                    child: _saving
                        ? const SizedBox(
                            width: 18,
                            height: 18,
                            child: CircularProgressIndicator(
                                strokeWidth: 2, color: Colors.white),
                          )
                        : const Text('Save'),
                  ),
                ],
              ),
            ],
          ),
        );
      },
    );
  }
}

class _PermissionRow extends StatelessWidget {
  final String label;
  final String value;
  final List<String> options;
  final ValueChanged<String> onChanged;

  const _PermissionRow({
    required this.label,
    required this.value,
    required this.options,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Row(
        children: [
          Expanded(child: Text(label, style: AppTextStyles.body)),
          DropdownButton<String>(
            value: options.contains(value) ? value : options.first,
            items: options
                .map((opt) =>
                    DropdownMenuItem(value: opt, child: Text(opt)))
                .toList(),
            onChanged: (val) {
              if (val != null) onChanged(val);
            },
          ),
        ],
      ),
    );
  }
}
