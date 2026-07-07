import 'package:feature_qa/ask_question_screen.dart';
import 'package:feature_qa/drafts_screen.dart';
import 'package:feature_qa/qa_feed_screen.dart';
import 'package:feature_qa/qa_profile_screen.dart';
import 'package:feature_qa/qa_search_screen.dart';
import 'package:feature_qa/question_detail_screen.dart';
import 'package:go_router/go_router.dart';

/// Q&A route table. Spread into the app router's shell.
List<RouteBase> qaRoutes() => [
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
    builder: (context, state) => QaProfileScreen(
      userId: state.pathParameters['userId']!,
    ),
  ),
];
