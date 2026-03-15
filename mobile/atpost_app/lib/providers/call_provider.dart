import 'package:atpost_app/data/models/call.dart';
import 'package:atpost_app/data/repositories/calls_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Fetches call history with pagination.
final callHistoryProvider =
    FutureProvider.autoDispose.family<List<CallHistoryItem>, String?>(
  (ref, cursor) async {
    final repo = ref.watch(callsRepositoryProvider);
    return repo.getCallHistory(cursor: cursor);
  },
);

/// Fetches details for a specific call.
final callDetailsProvider =
    FutureProvider.autoDispose.family<CallSession, String>(
  (ref, callId) async {
    final repo = ref.watch(callsRepositoryProvider);
    return repo.getCall(callId);
  },
);

/// Active call state — tracks the current call session.
final activeCallProvider =
    StateProvider<CallSession?>((ref) => null);
