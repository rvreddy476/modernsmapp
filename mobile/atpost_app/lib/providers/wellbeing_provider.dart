import 'package:atpost_app/data/models/wellbeing.dart';
import 'package:atpost_app/data/repositories/wellbeing_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final wellbeingProvider =
    FutureProvider.autoDispose<DigitalWellbeing>((ref) async {
  return ref.watch(wellbeingRepositoryProvider).getWellbeing();
});

final screenTimeProvider =
    FutureProvider.autoDispose<List<ScreenTimeLog>>((ref) async {
  return ref.watch(wellbeingRepositoryProvider).getScreenTime();
});
