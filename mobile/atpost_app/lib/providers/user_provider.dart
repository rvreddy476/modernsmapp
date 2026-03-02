import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Current user profile provider.
final currentUserProvider = FutureProvider.autoDispose<User>((ref) async {
  final repo = ref.watch(userRepositoryProvider);
  return repo.getMe();
});

/// Provider to fetch another user's profile by ID.
final userProfileProvider =
    FutureProvider.autoDispose.family<User, String>((ref, userId) async {
  final repo = ref.watch(userRepositoryProvider);
  return repo.getUser(userId);
});
