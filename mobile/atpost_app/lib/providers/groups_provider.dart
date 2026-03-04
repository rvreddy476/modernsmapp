import 'package:atpost_app/data/models/group.dart';
import 'package:atpost_app/data/repositories/groups_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final myGroupsProvider = FutureProvider.autoDispose<List<Group>>((ref) async {
  return ref.watch(groupsRepositoryProvider).getGroups(member: 'me');
});

final discoverGroupsProvider =
    FutureProvider.autoDispose<List<Group>>((ref) async {
  return ref
      .watch(groupsRepositoryProvider)
      .getGroups(type: 'public', sort: 'members');
});

final groupDetailProvider =
    FutureProvider.autoDispose.family<Group, String>((ref, groupId) async {
  return ref.watch(groupsRepositoryProvider).getGroup(groupId);
});
