import 'package:atpost_app/features/qa/ask_question_screen.dart';
import 'package:atpost_app/features/qa/drafts_screen.dart';
import 'package:atpost_app/features/qa/qa_feed_screen.dart';
import 'package:atpost_app/features/qa/qa_profile_screen.dart';
import 'package:atpost_app/features/qa/qa_search_screen.dart';
import 'package:atpost_app/features/qa/question_detail_screen.dart';
import 'package:go_router/go_router.dart';

class QARoutes {
  static List<RouteBase> get routes => [
        GoRoute(
          path: '/qa',
          builder: (context, state) => const QAFeedScreen(),
        ),
        GoRoute(
          path: '/qa/ask',
          builder: (context, state) => const AskQuestionScreen(),
        ),
        GoRoute(
          path: '/qa/question/:questionId',
          builder: (context, state) => QuestionDetailScreen(
            questionId: state.pathParameters['questionId']!,
          ),
        ),
        GoRoute(
          path: '/qa/search',
          builder: (context, state) => QaSearchScreen(
            initialQuery: state.uri.queryParameters['q'],
            communityId: state.uri.queryParameters['community_id'],
            topicId: state.uri.queryParameters['topic_id'],
          ),
        ),
        GoRoute(
          path: '/qa/drafts',
          builder: (_, _) => const QaDraftsScreen(),
        ),
        GoRoute(
          path: '/qa/profile/:userId',
          builder: (context, state) =>
              QaProfileScreen(userId: state.pathParameters['userId']!),
        ),
      ];
}
