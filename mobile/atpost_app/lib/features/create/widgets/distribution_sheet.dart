import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';

/// Module 1 P0-2 — creator publication choices.
///
/// PostTube and Reels get DIFFERENT option sets, so this widget is
/// configured by [surface] rather than being one sheet reused blindly:
///
///  * PostTube: visibility (public/unlisted/private/scheduled), "also show
///    in social feed" (default OFF — PostTube-only is the default), and
///    "notify subscribers" (default ON).
///  * Reels: main-feed is the point of a reel, so it defaults ON; there is
///    no scheduled/unlisted split in the reel composer today.
///
/// Scheduling is displayed in the device's local time and transmitted as
/// UTC (see [DistributionChoices.scheduleAtUtcIso]).
enum DistributionSurface { posttube, reel }

enum PublishVisibility { public, unlisted, private, scheduled }

class DistributionChoices {
  DistributionChoices({
    required this.visibility,
    required this.mainFeed,
    required this.notifySubscribers,
    this.scheduleAtLocal,
  });

  /// PostTube default: PostTube-only, subscribers notified.
  factory DistributionChoices.posttubeDefaults() => DistributionChoices(
        visibility: PublishVisibility.public,
        mainFeed: false,
        notifySubscribers: true,
      );

  /// Reel default: reels are a social-feed format.
  factory DistributionChoices.reelDefaults() => DistributionChoices(
        visibility: PublishVisibility.public,
        mainFeed: true,
        notifySubscribers: true,
      );

  PublishVisibility visibility;
  bool mainFeed;
  bool notifySubscribers;
  DateTime? scheduleAtLocal;

  /// Wire value for `posts.visibility`. A scheduled post is stored public
  /// and held by its schedule; visibility itself stays canonical.
  String get visibilityWire {
    switch (visibility) {
      case PublishVisibility.unlisted:
        return 'unlisted';
      case PublishVisibility.private:
        return 'private';
      case PublishVisibility.public:
      case PublishVisibility.scheduled:
        return 'public';
    }
  }

  /// UTC ISO-8601 for the server; null when publishing immediately.
  String? get scheduleAtUtcIso {
    if (visibility != PublishVisibility.scheduled) return null;
    return scheduleAtLocal?.toUtc().toIso8601String();
  }

  /// The typed, versioned policy object post-service expects (P0-1).
  /// `create_reel_preview` is deliberately omitted — the server rejects it
  /// with 400 until the feature ships, and sending false is noise.
  Map<String, dynamic> toPolicyJson() => {
        'version': 1,
        'main_feed': mainFeed,
        'notify_subscribers': notifySubscribers,
      };

  /// Restores choices after process death / upload retry (P0-2: choices
  /// must survive backgrounding).
  Map<String, dynamic> toJson() => {
        'visibility': visibility.name,
        'main_feed': mainFeed,
        'notify_subscribers': notifySubscribers,
        'schedule_at_local': scheduleAtLocal?.toIso8601String(),
      };

  static DistributionChoices fromJson(Map<String, dynamic> json) {
    return DistributionChoices(
      visibility: PublishVisibility.values.firstWhere(
        (v) => v.name == json['visibility'],
        orElse: () => PublishVisibility.public,
      ),
      mainFeed: json['main_feed'] == true,
      notifySubscribers: json['notify_subscribers'] != false,
      scheduleAtLocal: json['schedule_at_local'] != null
          ? DateTime.tryParse(json['schedule_at_local'] as String)
          : null,
    );
  }

  /// Blocks invalid combinations before they reach the server.
  String? validate() {
    if (visibility == PublishVisibility.scheduled) {
      if (scheduleAtLocal == null) {
        return 'Pick a date and time to schedule this post.';
      }
      if (scheduleAtLocal!.isBefore(DateTime.now())) {
        return 'Scheduled time must be in the future.';
      }
    }
    if (visibility == PublishVisibility.private && mainFeed) {
      return 'A private video cannot also appear in the social feed.';
    }
    if (visibility == PublishVisibility.unlisted && mainFeed) {
      return 'An unlisted video cannot appear in the social feed.';
    }
    return null;
  }

  DistributionChoices copy() => DistributionChoices(
        visibility: visibility,
        mainFeed: mainFeed,
        notifySubscribers: notifySubscribers,
        scheduleAtLocal: scheduleAtLocal,
      );
}

/// Bottom sheet for the creator's publication choices. Returns the edited
/// choices, or null if dismissed.
Future<DistributionChoices?> showDistributionSheet(
  BuildContext context, {
  required DistributionSurface surface,
  required DistributionChoices initial,
}) {
  return showModalBottomSheet<DistributionChoices>(
    context: context,
    isScrollControlled: true,
    backgroundColor: AppColors.bgSecondary,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
    ),
    builder: (ctx) => _DistributionSheet(surface: surface, initial: initial),
  );
}

class _DistributionSheet extends StatefulWidget {
  const _DistributionSheet({required this.surface, required this.initial});

  final DistributionSurface surface;
  final DistributionChoices initial;

  @override
  State<_DistributionSheet> createState() => _DistributionSheetState();
}

class _DistributionSheetState extends State<_DistributionSheet> {
  late DistributionChoices _choices = widget.initial.copy();
  String? _error;

  bool get _isPosttube => widget.surface == DistributionSurface.posttube;

  Future<void> _pickSchedule() async {
    final now = DateTime.now();
    final date = await showDatePicker(
      context: context,
      initialDate: _choices.scheduleAtLocal ?? now.add(const Duration(hours: 1)),
      firstDate: now,
      lastDate: now.add(const Duration(days: 365)),
    );
    if (date == null || !mounted) return;
    final time = await showTimePicker(
      context: context,
      initialTime: TimeOfDay.fromDateTime(
        _choices.scheduleAtLocal ?? now.add(const Duration(hours: 1)),
      ),
    );
    if (time == null || !mounted) return;
    setState(() {
      _choices.scheduleAtLocal =
          DateTime(date.year, date.month, date.day, time.hour, time.minute);
      _error = null;
    });
  }

  void _apply() {
    final err = _choices.validate();
    if (err != null) {
      setState(() => _error = err);
      return;
    }
    Navigator.of(context).pop(_choices);
  }

  @override
  Widget build(BuildContext context) {
    final scheduled = _choices.visibility == PublishVisibility.scheduled;
    return Padding(
      padding: EdgeInsets.only(
        left: AppSpacing.xxl,
        right: AppSpacing.xxl,
        top: AppSpacing.xxl,
        bottom: MediaQuery.of(context).viewInsets.bottom + AppSpacing.xxl,
      ),
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Publishing options', style: AppTextStyles.h3),
            const SizedBox(height: AppSpacing.l),
            Text(
              _isPosttube
                  ? 'Your video always lives on PostTube. Choose whether it also appears in the social feed.'
                  : 'Choose who can see this reel and where it appears.',
              style: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
            ),
            const SizedBox(height: AppSpacing.xxl),

            Text('Visibility', style: AppTextStyles.bodyMedium),
            const SizedBox(height: AppSpacing.xs),
            _VisibilityChoice(
              value: PublishVisibility.public,
              groupValue: _choices.visibility,
              label: 'Public',
              subtitle: 'Anyone can find and watch',
              onChanged: _setVisibility,
            ),
            if (_isPosttube)
              _VisibilityChoice(
                value: PublishVisibility.unlisted,
                groupValue: _choices.visibility,
                label: 'Unlisted',
                subtitle: 'Only people with the link — hidden from feeds and search',
                onChanged: _setVisibility,
              ),
            _VisibilityChoice(
              value: PublishVisibility.private,
              groupValue: _choices.visibility,
              label: 'Private',
              subtitle: 'Only you',
              onChanged: _setVisibility,
            ),
            if (_isPosttube)
              _VisibilityChoice(
                value: PublishVisibility.scheduled,
                groupValue: _choices.visibility,
                label: 'Schedule',
                subtitle: 'Publish automatically at a chosen time',
                onChanged: _setVisibility,
              ),

            if (scheduled) ...[
              const SizedBox(height: AppSpacing.l),
              OutlinedButton.icon(
                onPressed: _pickSchedule,
                icon: const Icon(Icons.schedule, size: 18),
                label: Text(
                  _choices.scheduleAtLocal == null
                      ? 'Pick date & time'
                      : _formatLocal(_choices.scheduleAtLocal!),
                ),
              ),
              const SizedBox(height: AppSpacing.xs),
              Text(
                'Shown in your local time.',
                style: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
              ),
            ],

            const Divider(height: AppSpacing.xxxxl),

            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              value: _choices.mainFeed,
              // Private/unlisted can't be surfaced socially — the toggle is
              // disabled rather than silently ignored on the server.
              onChanged: _canShareToFeed
                  ? (v) => setState(() {
                        _choices.mainFeed = v;
                        _error = null;
                      })
                  : null,
              title: const Text('Also show in social feed'),
              subtitle: Text(
                _canShareToFeed
                    ? (_isPosttube
                        ? 'Adds a reference in the feed. Your video stays on PostTube.'
                        : 'Show this reel in followers\' feeds.')
                    : 'Not available for private or unlisted videos.',
                style: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
              ),
            ),
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              value: _choices.notifySubscribers,
              onChanged: (v) => setState(() => _choices.notifySubscribers = v),
              title: const Text('Notify subscribers'),
              subtitle: Text(
                'Send a notification to people subscribed to your channel.',
                style: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
              ),
            ),

            if (_error != null) ...[
              const SizedBox(height: AppSpacing.l),
              Text(
                _error!,
                style: AppTextStyles.bodySmall.copyWith(color: AppColors.statusError),
              ),
            ],
            const SizedBox(height: AppSpacing.xxl),
            SizedBox(
              width: double.infinity,
              child: FilledButton(onPressed: _apply, child: const Text('Done')),
            ),
            const SizedBox(height: AppSpacing.l),
          ],
        ),
      ),
    );
  }

  bool get _canShareToFeed =>
      _choices.visibility == PublishVisibility.public ||
      _choices.visibility == PublishVisibility.scheduled;

  void _setVisibility(PublishVisibility v) {
    setState(() {
      _choices.visibility = v;
      if (!_canShareToFeed) _choices.mainFeed = false;
      _error = null;
    });
  }

  static String _formatLocal(DateTime dt) {
    String two(int n) => n.toString().padLeft(2, '0');
    return '${dt.year}-${two(dt.month)}-${two(dt.day)} ${two(dt.hour)}:${two(dt.minute)}';
  }
}

class _VisibilityChoice extends StatelessWidget {
  const _VisibilityChoice({
    required this.value,
    required this.groupValue,
    required this.label,
    required this.subtitle,
    required this.onChanged,
  });

  final PublishVisibility value;
  final PublishVisibility groupValue;
  final String label;
  final String subtitle;
  final ValueChanged<PublishVisibility> onChanged;

  @override
  Widget build(BuildContext context) {
    return RadioListTile<PublishVisibility>(
      contentPadding: EdgeInsets.zero,
      value: value,
      groupValue: groupValue,
      onChanged: (v) => v == null ? null : onChanged(v),
      title: Text(label, style: AppTextStyles.bodyMedium),
      subtitle: Text(
        subtitle,
        style: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
      ),
    );
  }
}
