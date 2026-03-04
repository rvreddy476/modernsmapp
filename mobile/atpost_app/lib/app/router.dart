import 'package:atpost_app/features/chat/chat_detail_screen.dart';
import 'package:atpost_app/features/chat/chat_list_screen.dart';
import 'package:atpost_app/features/live/live_screen.dart';
import 'package:atpost_app/features/memories/memories_screen.dart';
import 'package:atpost_app/features/profile/profile_detail_screen.dart';
import 'package:atpost_app/features/posttube/posttube_screen.dart';
import 'package:atpost_app/features/reels/reels_screen.dart';
import 'package:atpost_app/features/shell/shell_scaffold.dart';
import 'package:atpost_app/features/shop/shop_screen.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

final appRouterProvider = Provider<GoRouter>((ref) {
  return GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const ShellScaffold(),
      ),
      GoRoute(
        path: '/chat',
        builder: (context, state) => const ChatListScreen(),
      ),
      GoRoute(
        path: '/chat/:conversationId',
        builder: (context, state) => ChatDetailScreen(
          conversationId: state.pathParameters['conversationId'] ?? 'general',
        ),
      ),
      GoRoute(
        path: '/posttube',
        builder: (context, state) => const PosttubeScreen(),
      ),
      GoRoute(
        path: '/reels',
        builder: (context, state) => const ReelsScreen(fullscreenRoute: true),
      ),
      GoRoute(
        path: '/shop',
        builder: (context, state) => const ShopScreen(),
      ),
      GoRoute(
        path: '/memories',
        builder: (context, state) => const MemoriesScreen(),
      ),
      GoRoute(
        path: '/live',
        builder: (context, state) => const LiveScreen(),
      ),
      GoRoute(
        path: '/profile/:userId',
        builder: (context, state) => ProfileDetailScreen(
          userId: state.pathParameters['userId'] ?? '',
        ),
      ),
    ],
  );
});

