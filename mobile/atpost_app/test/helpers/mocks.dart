import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:atpost_app/data/repositories/feed_repository.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_network/api_client.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_realtime/realtime_service.dart';
import 'package:mocktail/mocktail.dart';

class MockApiClient extends Mock implements ApiClient {}

class MockAuthService extends Mock implements AuthService {}

class MockFeedRepository extends Mock implements FeedRepository {}

class MockUserRepository extends Mock implements UserRepository {}

class MockPostRepository extends Mock implements PostRepository {}

class MockChatRepository extends Mock implements ChatRepository {}

class MockRealtimeService extends Mock implements RealtimeService {}
