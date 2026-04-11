import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/data/models/community.dart';
import 'package:atpost_app/data/repositories/communities_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// State for managing communities with optimistic update support.
class CommunitiesState {
  final List<Community> myCommunities;
  final List<Community> discoveredCommunities;

  const CommunitiesState({
    this.myCommunities = const [],
    this.discoveredCommunities = const [],
  });

  CommunitiesState copyWith({
    List<Community>? myCommunities,
    List<Community>? discoveredCommunities,
  }) {
    return CommunitiesState(
      myCommunities: myCommunities ?? this.myCommunities,
      discoveredCommunities:
          discoveredCommunities ?? this.discoveredCommunities,
    );
  }
}

/// Advanced Communities Notifier for production scale.
class CommunitiesNotifier extends StateNotifier<AsyncValue<CommunitiesState>> {
  final CommunitiesRepository _repo;

  CommunitiesNotifier(this._repo) : super(const AsyncValue.loading()) {
    refresh();
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    try {
      final results = await Future.wait([
        ErrorHandler.retry(() => _repo.getMyCommunities()),
        ErrorHandler.retry(() => _repo.discoverCommunities()),
      ]);

      state = AsyncValue.data(
        CommunitiesState(
          myCommunities: results[0],
          discoveredCommunities: results[1],
        ),
      );
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  /// Optimistically toggles the join status of a community.
  Future<void> toggleJoin(String communityId) async {
    final currentState = state.value;
    if (currentState == null) return;

    Community? target;
    bool inMyList = true;
    int index = currentState.myCommunities.indexWhere(
      (c) => c.id == communityId,
    );

    if (index != -1) {
      target = currentState.myCommunities[index];
    } else {
      index = currentState.discoveredCommunities.indexWhere(
        (c) => c.id == communityId,
      );
      if (index != -1) {
        target = currentState.discoveredCommunities[index];
        inMyList = false;
      }
    }

    if (target == null) return;

    final wasJoined =
        target.viewerRole != null && target.viewerRole != 'outsider';
    final updated = target.copyWith(
      viewerRole: wasJoined ? 'outsider' : 'member',
      memberCount: wasJoined
          ? (target.memberCount > 0 ? target.memberCount - 1 : 0)
          : target.memberCount + 1,
    );

    // 1. Optimistic Update
    final newMy = List<Community>.from(currentState.myCommunities);
    final newDiscover = List<Community>.from(
      currentState.discoveredCommunities,
    );

    if (inMyList) {
      if (updated.viewerRole == 'outsider') {
        newMy.removeAt(index);
      } else {
        newMy[index] = updated;
      }
    } else {
      if (updated.viewerRole != 'outsider') {
        newMy.insert(0, updated);
        newDiscover.removeAt(index);
      } else {
        newDiscover[index] = updated;
      }
    }

    state = AsyncValue.data(
      currentState.copyWith(
        myCommunities: newMy,
        discoveredCommunities: newDiscover,
      ),
    );

    // 2. Perform API call
    try {
      if (wasJoined) {
        await _repo.leave(communityId);
      } else {
        await _repo.join(communityId);
      }
    } catch (e) {
      // 3. Rollback
      state = AsyncValue.data(currentState);
      ErrorHandler.handle(
        e,
        StackTrace.current,
        context: 'CommunitiesNotifier.toggleJoin',
      );
    }
  }
}

final communitiesProvider =
    StateNotifierProvider.autoDispose<
      CommunitiesNotifier,
      AsyncValue<CommunitiesState>
    >((ref) {
      return CommunitiesNotifier(ref.watch(communitiesRepositoryProvider));
    });

/// Legacy compatibility providers
final myCommunitiesProvider = Provider.autoDispose<AsyncValue<List<Community>>>(
  (ref) {
    return ref.watch(communitiesProvider).whenData((s) => s.myCommunities);
  },
);

final discoverCommunitiesProvider =
    Provider.autoDispose<AsyncValue<List<Community>>>((ref) {
      return ref
          .watch(communitiesProvider)
          .whenData((s) => s.discoveredCommunities);
    });

final communityDetailProvider = FutureProvider.autoDispose
    .family<Community, String>((ref, communityId) async {
      return ref.watch(communitiesRepositoryProvider).getCommunity(communityId);
    });

final communitySpacesProvider = FutureProvider.autoDispose
    .family<List<CommunitySpace>, String>((ref, communityId) async {
      return ref.watch(communitiesRepositoryProvider).getSpaces(communityId);
    });

extension CommunityExtension on Community {
  Community copyWith({String? viewerRole, int? memberCount}) {
    return Community(
      id: id,
      name: name,
      handle: handle,
      description: description,
      communityType: communityType,
      status: status,
      spaceCount: spaceCount,
      memberCount: memberCount ?? this.memberCount,
      isVerified: isVerified,
      avatarMediaId: avatarMediaId,
      bannerMediaId: bannerMediaId,
      viewerRole: viewerRole ?? this.viewerRole,
      topicTags: topicTags,
      createdAt: createdAt,
    );
  }
}
