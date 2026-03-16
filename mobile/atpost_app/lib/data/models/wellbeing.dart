class DigitalWellbeing {
  final String userId;
  final int dailyLimitMins;
  final bool focusModeEnabled;
  final String? focusModeStart;
  final String? focusModeEnd;
  final bool bedtimeEnabled;
  final String? bedtimeStart;
  final String? bedtimeEnd;
  final bool hideLikeCounts;
  final bool hideViewCounts;
  final bool breakRemindersEnabled;
  final int breakIntervalMins;

  const DigitalWellbeing({
    required this.userId,
    required this.dailyLimitMins,
    required this.focusModeEnabled,
    this.focusModeStart,
    this.focusModeEnd,
    required this.bedtimeEnabled,
    this.bedtimeStart,
    this.bedtimeEnd,
    required this.hideLikeCounts,
    required this.hideViewCounts,
    required this.breakRemindersEnabled,
    required this.breakIntervalMins,
  });

  factory DigitalWellbeing.fromJson(Map<String, dynamic> json) =>
      DigitalWellbeing(
        userId: json['user_id'] as String? ?? '',
        dailyLimitMins: json['daily_limit_mins'] as int? ?? 0,
        focusModeEnabled: json['focus_mode_enabled'] as bool? ?? false,
        focusModeStart: json['focus_mode_start'] as String?,
        focusModeEnd: json['focus_mode_end'] as String?,
        bedtimeEnabled: json['bedtime_enabled'] as bool? ?? false,
        bedtimeStart: json['bedtime_start'] as String?,
        bedtimeEnd: json['bedtime_end'] as String?,
        hideLikeCounts: json['hide_like_counts'] as bool? ?? false,
        hideViewCounts: json['hide_view_counts'] as bool? ?? false,
        breakRemindersEnabled: json['break_reminders_enabled'] as bool? ?? false,
        breakIntervalMins: json['break_interval_mins'] as int? ?? 60,
      );

  Map<String, dynamic> toJson() => {
        'user_id': userId,
        'daily_limit_mins': dailyLimitMins,
        'focus_mode_enabled': focusModeEnabled,
        'focus_mode_start': focusModeStart,
        'focus_mode_end': focusModeEnd,
        'bedtime_enabled': bedtimeEnabled,
        'bedtime_start': bedtimeStart,
        'bedtime_end': bedtimeEnd,
        'hide_like_counts': hideLikeCounts,
        'hide_view_counts': hideViewCounts,
        'break_reminders_enabled': breakRemindersEnabled,
        'break_interval_mins': breakIntervalMins,
      };
}

class ScreenTimeLog {
  final String date;
  final int minutesActive;

  const ScreenTimeLog({
    required this.date,
    required this.minutesActive,
  });

  factory ScreenTimeLog.fromJson(Map<String, dynamic> json) => ScreenTimeLog(
        date: json['date'] as String? ?? '',
        minutesActive: json['minutes_active'] as int? ?? 0,
      );
}
