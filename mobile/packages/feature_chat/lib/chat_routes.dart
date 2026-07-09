import 'package:feature_chat/chat_detail_screen.dart';
import 'package:feature_chat/chat_list_screen.dart';
import 'package:feature_chat/message_requests_screen.dart';
import 'package:go_router/go_router.dart';

/// Chat route table (conversation list, message requests, detail).
/// Spread into the app router's shell.
List<RouteBase> chatRoutes() => [
  GoRoute(
    path: '/chat',
    builder: (context, state) => const ChatListScreen(),
  ),
  GoRoute(
    path: '/chat/requests',
    builder: (context, state) => const MessageRequestsScreen(),
  ),
  GoRoute(
    path: '/chat/:conversationId',
    builder: (context, state) => ChatDetailScreen(
      conversationId: state.pathParameters['conversationId'] ?? 'general',
    ),
  ),
];
