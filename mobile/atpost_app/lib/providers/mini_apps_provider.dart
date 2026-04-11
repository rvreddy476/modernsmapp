import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/data/models/mini_app.dart';
import 'package:atpost_app/data/repositories/mini_apps_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

const _selectedCategoryUnset = Object();

/// State for the Mini Apps ecosystem with optimistic updates.
class MiniAppsState {
  final List<MiniApp> allApps;
  final List<MiniApp> installedApps;
  final String? selectedCategory;

  const MiniAppsState({
    this.allApps = const [],
    this.installedApps = const [],
    this.selectedCategory,
  });

  MiniAppsState copyWith({
    List<MiniApp>? allApps,
    List<MiniApp>? installedApps,
    Object? selectedCategory = _selectedCategoryUnset,
  }) {
    return MiniAppsState(
      allApps: allApps ?? this.allApps,
      installedApps: installedApps ?? this.installedApps,
      selectedCategory: identical(selectedCategory, _selectedCategoryUnset)
          ? this.selectedCategory
          : selectedCategory as String?,
    );
  }
}

/// Production-ready Mini Apps Notifier.
class MiniAppsNotifier extends StateNotifier<AsyncValue<MiniAppsState>> {
  final MiniAppsRepository _repo;

  MiniAppsNotifier(this._repo) : super(const AsyncValue.loading()) {
    refresh();
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    try {
      final results = await Future.wait([
        ErrorHandler.retry(() => _repo.listApps()),
        ErrorHandler.retry(() => _repo.getInstalledApps()),
      ]);

      final all = results[0];
      final installed = results[1]
          .map((app) => app.copyWith(isInstalled: true))
          .toList();
      final installedById = {for (final app in installed) app.id: app};

      // Sync installation status into the "all" list
      final syncedAll = all
          .map((app) => app.withInstalledStateFrom(installedById[app.id]))
          .toList();

      state = AsyncValue.data(
        MiniAppsState(allApps: syncedAll, installedApps: installed),
      );
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  Future<void> setCategory(String? category) async {
    final currentState = state.value;
    if (currentState == null) return;

    state = AsyncValue.data(currentState.copyWith(selectedCategory: category));
    // Load filtered apps from API
    try {
      final filtered = await ErrorHandler.retry(
        () => _repo.listApps(category: category),
      );
      final installedById = {
        for (final app in currentState.installedApps) app.id: app,
      };
      final synced = filtered
          .map((app) => app.withInstalledStateFrom(installedById[app.id]))
          .toList();

      state = AsyncValue.data(state.value!.copyWith(allApps: synced));
    } catch (e, st) {
      ErrorHandler.handle(e, st, context: 'MiniApps.setCategory');
    }
  }

  Future<bool> installApp(
    String appId, {
    List<String> grantedPermissions = const [],
  }) async {
    final currentState = state.value;
    if (currentState == null) {
      try {
        await _repo.installApp(appId, grantedPermissions: grantedPermissions);
        await refresh();
        return true;
      } catch (e, st) {
        ErrorHandler.handle(e, st, context: 'MiniApps.installApp');
        return false;
      }
    }

    final index = currentState.allApps.indexWhere((a) => a.id == appId);
    if (index == -1) {
      try {
        await _repo.installApp(appId, grantedPermissions: grantedPermissions);
        await refresh();
        return true;
      } catch (e, st) {
        ErrorHandler.handle(e, st, context: 'MiniApps.installApp');
        return false;
      }
    }

    final targetApp = currentState.allApps[index];
    if (targetApp.isInstalled) return true;

    final updatedApp = targetApp.copyWith(
      isInstalled: true,
      installCount: targetApp.installCount + 1,
      grantedPermissions: grantedPermissions,
    );

    final newAll = List<MiniApp>.from(currentState.allApps)
      ..[index] = updatedApp;
    final newInstalled = List<MiniApp>.from(currentState.installedApps);
    final installedIndex = newInstalled.indexWhere((a) => a.id == appId);
    if (installedIndex == -1) {
      newInstalled.add(updatedApp);
    } else {
      newInstalled[installedIndex] = updatedApp;
    }

    state = AsyncValue.data(
      currentState.copyWith(allApps: newAll, installedApps: newInstalled),
    );

    try {
      await _repo.installApp(appId, grantedPermissions: grantedPermissions);
      return true;
    } catch (e, st) {
      state = AsyncValue.data(currentState);
      ErrorHandler.handle(e, st, context: 'MiniApps.installApp');
      return false;
    }
  }

  Future<bool> uninstallApp(String appId) async {
    final currentState = state.value;
    if (currentState == null) {
      try {
        await _repo.uninstallApp(appId);
        await refresh();
        return true;
      } catch (e, st) {
        ErrorHandler.handle(e, st, context: 'MiniApps.uninstallApp');
        return false;
      }
    }

    final index = currentState.allApps.indexWhere((a) => a.id == appId);
    if (index == -1) {
      try {
        await _repo.uninstallApp(appId);
        await refresh();
        return true;
      } catch (e, st) {
        ErrorHandler.handle(e, st, context: 'MiniApps.uninstallApp');
        return false;
      }
    }

    final targetApp = currentState.allApps[index];
    if (!targetApp.isInstalled) return true;

    final updatedApp = targetApp.copyWith(
      isInstalled: false,
      grantedPermissions: const [],
    );

    final newAll = List<MiniApp>.from(currentState.allApps)
      ..[index] = updatedApp;
    final newInstalled = List<MiniApp>.from(currentState.installedApps)
      ..removeWhere((a) => a.id == appId);

    state = AsyncValue.data(
      currentState.copyWith(allApps: newAll, installedApps: newInstalled),
    );

    try {
      await _repo.uninstallApp(appId);
      return true;
    } catch (e, st) {
      state = AsyncValue.data(currentState);
      ErrorHandler.handle(e, st, context: 'MiniApps.uninstallApp');
      return false;
    }
  }
}

final miniAppsProvider =
    StateNotifierProvider.autoDispose<
      MiniAppsNotifier,
      AsyncValue<MiniAppsState>
    >((ref) {
      return MiniAppsNotifier(ref.watch(miniAppsRepositoryProvider));
    });
