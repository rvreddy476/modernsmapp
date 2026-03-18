import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import '../../helpers/mocks.dart';
import '../../helpers/test_utils.dart';

void main() {
  late MockApiClient mockApi;
  late UserRepository repo;

  setUp(() {
    mockApi = MockApiClient();
    repo = UserRepository(mockApi);
  });

  group('getMe', () {
    test('parses user from response data', () async {
      when(() => mockApi.get(any()))
          .thenAnswer((_) async => mockResponse(data: {
                'data': {
                  'id': 'user-1',
                  'username': 'alice',
                  'display_name': 'Alice Smith',
                  'bio': 'Hello!',
                  'follower_count': 150,
                },
              }));

      final user = await repo.getMe();
      expect(user.id, 'user-1');
      expect(user.username, 'alice');
      expect(user.displayName, 'Alice Smith');
      expect(user.followerCount, 150);
    });
  });

  group('getUser', () {
    test('fetches user by ID', () async {
      when(() => mockApi.get(any()))
          .thenAnswer((_) async => mockResponse(data: {
                'data': {
                  'id': 'user-42',
                  'username': 'bob',
                  'display_name': 'Bob',
                },
              }));

      final user = await repo.getUser('user-42');
      expect(user.id, 'user-42');
      expect(user.username, 'bob');
    });
  });

  group('followUser / unfollowUser', () {
    test('followUser calls correct endpoint with data', () async {
      when(() => mockApi.post(any(), data: any(named: 'data')))
          .thenAnswer((_) async => mockResponse(data: {}));

      await repo.followUser('target-user');

      verify(() => mockApi.post(any(), data: {'followee_id': 'target-user'})).called(1);
    });

    test('unfollowUser calls correct endpoint with data', () async {
      when(() => mockApi.post(any(), data: any(named: 'data')))
          .thenAnswer((_) async => mockResponse(data: {}));

      await repo.unfollowUser('target-user');

      verify(() => mockApi.post(any(), data: {'followee_id': 'target-user'})).called(1);
    });
  });

  group('getUsersBatch', () {
    test('returns list of users from batch endpoint', () async {
      when(() => mockApi.post(any(), data: any(named: 'data')))
          .thenAnswer((_) async => mockResponse(data: {
                'profiles': [
                  {'id': 'u1', 'username': 'a', 'display_name': 'A'},
                  {'id': 'u2', 'username': 'b', 'display_name': 'B'},
                ],
              }));

      final users = await repo.getUsersBatch(['u1', 'u2']);
      expect(users.length, 2);
      expect(users[0].id, 'u1');
    });

    test('returns empty list when profiles is null', () async {
      when(() => mockApi.post(any(), data: any(named: 'data')))
          .thenAnswer((_) async => mockResponse(data: {}));

      final users = await repo.getUsersBatch(['u1']);
      expect(users, isEmpty);
    });
  });
}
