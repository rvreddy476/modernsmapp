import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/data/models/group.dart';
import 'package:atpost_app/data/repositories/groups_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// State for managing groups with optimistic update support.
class GroupsState {
  final List<Group> myGroups;
  final List<Group> discoveredGroups;
  final bool isLoading;

  const GroupsState({
    this.myGroups = const [],
    this.discoveredGroups = const [],
    this.isLoading = false,
  });

  GroupsState copyWith({
    List<Group>? myGroups,
    List<Group>? discoveredGroups,
    bool? isLoading,
  }) {
    return GroupsState(
      myGroups: myGroups ?? this.myGroups,
      discoveredGroups: discoveredGroups ?? this.discoveredGroups,
      isLoading: isLoading ?? this.isLoading,
    );
  }
}

/// Advanced Groups Notifier for production scale.
class GroupsNotifier extends StateNotifier<AsyncValue<GroupsState>> {
  final GroupsRepository _repo;

  GroupsNotifier(this._repo) : super(const AsyncValue.loading()) {
    refresh();
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    try {
      final results = await Future.wait([
        ErrorHandler.retry(() => _repo.getGroups(member: 'me')),
        ErrorHandler.retry(
          () => _repo.getGroups(type: 'public', sort: 'members'),
        ),
      ]);

      state = AsyncValue.data(
        GroupsState(myGroups: results[0], discoveredGroups: results[1]),
      );
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  /// Optimistically toggles the join status of a group.
  Future<void> toggleJoin(String groupId) async {
    final currentState = state.value;
    if (currentState == null) return;

    // Find the group in either list
    Group? targetGroup;
    bool inMyGroups = true;
    int index = currentState.myGroups.indexWhere((g) => g.id == groupId);

    if (index != -1) {
      targetGroup = currentState.myGroups[index];
    } else {
      index = currentState.discoveredGroups.indexWhere((g) => g.id == groupId);
      if (index != -1) {
        targetGroup = currentState.discoveredGroups[index];
        inMyGroups = false;
      }
    }

    if (targetGroup == null) return;

    final wasJoined = targetGroup.isMember;
    final updatedGroup = targetGroup.copyWith(
      isMember: !wasJoined,
      memberCount: wasJoined
          ? targetGroup.memberCount - 1
          : targetGroup.memberCount + 1,
    );

    // 1. Optimistic Update
    final newMyGroups = List<Group>.from(currentState.myGroups);
    final newDiscoverGroups = List<Group>.from(currentState.discoveredGroups);

    if (inMyGroups) {
      if (!updatedGroup.isMember) {
        newMyGroups.removeAt(index); // Remove from "My Groups" if left
      } else {
        newMyGroups[index] = updatedGroup;
      }
    } else {
      if (updatedGroup.isMember) {
        newMyGroups.insert(0, updatedGroup); // Add to "My Groups" if joined
        newDiscoverGroups.removeAt(index);
      } else {
        newDiscoverGroups[index] = updatedGroup;
      }
    }

    state = AsyncValue.data(
      currentState.copyWith(
        myGroups: newMyGroups,
        discoveredGroups: newDiscoverGroups,
      ),
    );

    // 2. Perform API call
    try {
      if (wasJoined) {
        await _repo.leaveGroup(groupId);
      } else {
        await _repo.joinGroup(groupId);
      }
    } catch (e) {
      // 3. Rollback on failure
      state = AsyncValue.data(currentState);
      ErrorHandler.handle(
        e,
        StackTrace.current,
        context: 'GroupsNotifier.toggleJoin',
      );
    }
  }
}

final groupsProvider =
    StateNotifierProvider.autoDispose<GroupsNotifier, AsyncValue<GroupsState>>((
      ref,
    ) {
      return GroupsNotifier(ref.watch(groupsRepositoryProvider));
    });

/// Legacy compatibility providers (refactored to use the central state)
final myGroupsProvider = Provider.autoDispose<AsyncValue<List<Group>>>((ref) {
  return ref.watch(groupsProvider).whenData((s) => s.myGroups);
});

final discoverGroupsProvider = Provider.autoDispose<AsyncValue<List<Group>>>((
  ref,
) {
  return ref.watch(groupsProvider).whenData((s) => s.discoveredGroups);
});

final groupDetailProvider = FutureProvider.autoDispose.family<Group, String>((
  ref,
  groupId,
) async {
  return ref.watch(groupsRepositoryProvider).getGroup(groupId);
});

extension GroupExtension on Group {
  Group copyWith({bool? isMember, int? memberCount}) {
    return Group(
      id: id,
      name: name,
      description: description,
      privacy: privacy,
      coverMediaId: coverMediaId,
      creatorId: creatorId,
      memberCount: memberCount ?? this.memberCount,
      postCount: postCount,
      isMember: isMember ?? this.isMember,
      isAdmin: isAdmin,
      createdAt: createdAt,
    );
  }
}
