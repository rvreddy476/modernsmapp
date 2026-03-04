import 'package:atpost_app/data/models/conversation.dart';
import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final chatConversationsProvider =
    FutureProvider.autoDispose<List<Conversation>>((ref) async {
  final repo = ref.watch(chatRepositoryProvider);
  return repo.getConversations();
});
