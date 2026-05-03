import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/story.dart';
import 'package:flutter/material.dart';

/// Bottom sheet shown from the story creator. Lets the author build one of:
///   poll | quiz | countdown | question | slider
/// and returns a [StoryInteractive] draft (no `id` yet).
class InteractiveComposerSheet extends StatefulWidget {
  const InteractiveComposerSheet({super.key});

  static Future<StoryInteractive?> show(BuildContext context) {
    return showModalBottomSheet<StoryInteractive>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      isScrollControlled: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => const InteractiveComposerSheet(),
    );
  }

  @override
  State<InteractiveComposerSheet> createState() =>
      _InteractiveComposerSheetState();
}

class _InteractiveComposerSheetState extends State<InteractiveComposerSheet> {
  String _type = 'poll';

  final TextEditingController _question = TextEditingController();
  final List<TextEditingController> _options = [
    TextEditingController(),
    TextEditingController(),
  ];
  int _correctIdx = 0;
  DateTime? _countdownAt;
  String _emoji = '😍';
  final TextEditingController _emojiController =
      TextEditingController(text: '😍');

  @override
  void dispose() {
    _question.dispose();
    for (final c in _options) {
      c.dispose();
    }
    _emojiController.dispose();
    super.dispose();
  }

  void _addOption() {
    if (_options.length >= 4) return;
    setState(() => _options.add(TextEditingController()));
  }

  void _removeOption(int idx) {
    if (_options.length <= 2) return;
    setState(() {
      _options[idx].dispose();
      _options.removeAt(idx);
      if (_correctIdx >= _options.length) {
        _correctIdx = _options.length - 1;
      }
    });
  }

  Future<void> _pickCountdown() async {
    final now = DateTime.now();
    final date = await showDatePicker(
      context: context,
      initialDate: now.add(const Duration(days: 1)),
      firstDate: now,
      lastDate: now.add(const Duration(days: 365)),
    );
    if (date == null || !mounted) return;
    final time = await showTimePicker(
      context: context,
      initialTime: TimeOfDay.now(),
    );
    if (time == null) return;
    setState(() {
      _countdownAt = DateTime(
        date.year,
        date.month,
        date.day,
        time.hour,
        time.minute,
      );
    });
  }

  StoryInteractive? _build() {
    final question = _question.text.trim();
    if (question.isEmpty) return null;

    switch (_type) {
      case 'poll':
      case 'quiz':
        final opts = <StoryInteractiveOption>[];
        for (var i = 0; i < _options.length; i++) {
          final txt = _options[i].text.trim();
          if (txt.isEmpty) return null;
          opts.add(StoryInteractiveOption(id: 'opt_$i', text: txt));
        }
        return StoryInteractive(
          id: '',
          type: _type,
          question: question,
          options: opts,
          correctIdx: _type == 'quiz' ? _correctIdx : null,
        );
      case 'countdown':
        if (_countdownAt == null) return null;
        return StoryInteractive(
          id: '',
          type: _type,
          question: question,
          endTime: _countdownAt,
        );
      case 'question':
        return StoryInteractive(
          id: '',
          type: _type,
          question: question,
        );
      case 'slider':
        return StoryInteractive(
          id: '',
          type: _type,
          question: question,
          emoji: _emoji.isEmpty ? '😍' : _emoji,
        );
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    final bottomInset = MediaQuery.of(context).viewInsets.bottom;
    return Padding(
      padding: EdgeInsets.only(bottom: bottomInset),
      child: SafeArea(
        child: SingleChildScrollView(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(20, 12, 20, 20),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Container(
                  width: 40,
                  height: 4,
                  margin: const EdgeInsets.only(bottom: 12),
                  decoration: BoxDecoration(
                    color: AppColors.borderMedium,
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
                Center(
                    child: Text('Add interactive', style: AppTextStyles.h2)),
                const SizedBox(height: 12),
                _TypePicker(
                  selected: _type,
                  onSelect: (t) => setState(() => _type = t),
                ),
                const SizedBox(height: 16),
                _LabeledField(
                  label: _type == 'question' ? 'Prompt' : 'Question',
                  controller: _question,
                  maxLength: 120,
                ),
                const SizedBox(height: 12),
                if (_type == 'poll' || _type == 'quiz') ..._buildOptions(),
                if (_type == 'countdown') _buildCountdown(),
                if (_type == 'slider') _buildSlider(),
                const SizedBox(height: 16),
                Row(
                  children: [
                    Expanded(
                      child: TextButton(
                        onPressed: () => Navigator.pop(context),
                        child:
                            Text('Cancel', style: AppTextStyles.label),
                      ),
                    ),
                    Expanded(
                      child: ElevatedButton(
                        style: ElevatedButton.styleFrom(
                          backgroundColor: AppColors.postbookPrimary,
                          padding: const EdgeInsets.symmetric(vertical: 12),
                        ),
                        onPressed: () {
                          final draft = _build();
                          if (draft == null) {
                            ScaffoldMessenger.of(context).showSnackBar(
                              const SnackBar(
                                content: Text(
                                  'Please fill in the question and all options.',
                                ),
                              ),
                            );
                            return;
                          }
                          Navigator.pop(context, draft);
                        },
                        child: Text(
                          'Add to story',
                          style: AppTextStyles.label
                              .copyWith(color: Colors.white),
                        ),
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  List<Widget> _buildOptions() {
    return [
      for (var i = 0; i < _options.length; i++)
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: Row(
            children: [
              if (_type == 'quiz')
                Radio<int>(
                  value: i,
                  groupValue: _correctIdx,
                  onChanged: (v) => setState(() => _correctIdx = v ?? 0),
                  activeColor: AppColors.statusSuccess,
                ),
              Expanded(
                child: _LabeledField(
                  label: 'Option ${i + 1}',
                  controller: _options[i],
                  maxLength: 60,
                ),
              ),
              if (_options.length > 2)
                IconButton(
                  icon: const Icon(Icons.close,
                      color: AppColors.textMuted, size: 18),
                  onPressed: () => _removeOption(i),
                ),
            ],
          ),
        ),
      if (_options.length < 4)
        Align(
          alignment: Alignment.centerLeft,
          child: TextButton.icon(
            icon: const Icon(Icons.add, color: AppColors.postbookPrimary),
            label: Text('Add option',
                style: AppTextStyles.label
                    .copyWith(color: AppColors.postbookPrimary)),
            onPressed: _addOption,
          ),
        ),
    ];
  }

  Widget _buildCountdown() {
    final formatted = _countdownAt == null
        ? 'Pick a target time'
        : '${_countdownAt!.toLocal()}'.split('.').first;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: OutlinedButton.icon(
        onPressed: _pickCountdown,
        style: OutlinedButton.styleFrom(
          padding: const EdgeInsets.symmetric(vertical: 14),
        ),
        icon: const Icon(Icons.schedule, color: AppColors.postbookPrimary),
        label: Text(formatted, style: AppTextStyles.bodyMedium),
      ),
    );
  }

  Widget _buildSlider() {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        children: [
          SizedBox(
            width: 80,
            child: TextField(
              controller: _emojiController,
              maxLength: 2,
              textAlign: TextAlign.center,
              style: const TextStyle(fontSize: 28),
              decoration: const InputDecoration(
                counterText: '',
                border: OutlineInputBorder(),
              ),
              onChanged: (v) => _emoji = v.isEmpty ? '😍' : v,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              'Pick an emoji that fits the question.',
              style: AppTextStyles.bodySmall,
            ),
          ),
        ],
      ),
    );
  }
}

class _TypePicker extends StatelessWidget {
  const _TypePicker({required this.selected, required this.onSelect});

  final String selected;
  final ValueChanged<String> onSelect;

  static const _types = <Map<String, dynamic>>[
    {'key': 'poll', 'label': 'Poll', 'icon': Icons.poll_outlined},
    {'key': 'quiz', 'label': 'Quiz', 'icon': Icons.psychology_outlined},
    {
      'key': 'countdown',
      'label': 'Countdown',
      'icon': Icons.timer_outlined,
    },
    {
      'key': 'question',
      'label': 'Question',
      'icon': Icons.help_outline,
    },
    {
      'key': 'slider',
      'label': 'Slider',
      'icon': Icons.linear_scale,
    },
  ];

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 40,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        itemCount: _types.length,
        separatorBuilder: (_, _) => const SizedBox(width: 8),
        itemBuilder: (context, i) {
          final t = _types[i];
          final isSelected = selected == t['key'];
          return ChoiceChip(
            avatar: Icon(t['icon'] as IconData,
                size: 16,
                color: isSelected
                    ? Colors.white
                    : AppColors.textSecondary),
            label: Text(t['label'] as String),
            selected: isSelected,
            onSelected: (_) => onSelect(t['key'] as String),
            selectedColor: AppColors.postbookPrimary,
            labelStyle: AppTextStyles.label.copyWith(
              color: isSelected ? Colors.white : AppColors.textSecondary,
            ),
            backgroundColor: AppColors.bgTertiary,
          );
        },
      ),
    );
  }
}

class _LabeledField extends StatelessWidget {
  const _LabeledField({
    required this.label,
    required this.controller,
    this.maxLength,
  });

  final String label;
  final TextEditingController controller;
  final int? maxLength;

  @override
  Widget build(BuildContext context) {
    return TextField(
      controller: controller,
      maxLength: maxLength,
      style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
      decoration: InputDecoration(
        labelText: label,
        labelStyle: AppTextStyles.label,
        counterStyle: AppTextStyles.labelTiny,
        filled: true,
        fillColor: AppColors.bgTertiary,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(10),
          borderSide: BorderSide.none,
        ),
      ),
    );
  }
}
