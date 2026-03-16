import 'package:atpost_app/data/models/mini_app.dart';
import 'package:atpost_app/data/repositories/mini_apps_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final miniAppsProvider = FutureProvider.autoDispose
    .family<List<MiniApp>, String?>((ref, category) async {
  return ref.watch(miniAppsRepositoryProvider).listApps(category: category);
});

final installedAppsProvider =
    FutureProvider.autoDispose<List<MiniApp>>((ref) async {
  return ref.watch(miniAppsRepositoryProvider).getInstalledApps();
});
