import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/monetization.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('creator ledger keeps exact integer paise and metadata', () {
    final ledger = EarningsSummary.fromJson({
      'balance_paise': 12345,
      'pending_payout_paise': 4567,
      'lifetime_earnings_paise': 987654,
      'currency': 'INR',
      'is_frozen': false,
      'has_activity': true,
      'updated_at': '2026-08-12T12:00:00Z',
    });

    expect(ledger.availablePaise, 12345);
    expect(ledger.pendingPayoutPaise, 4567);
    expect(ledger.lifetimeEarningsPaise, 987654);
    expect(ledger.hasActivity, isTrue);
    expect(ledger.formattedAvailable, '₹ 123.45');
    expect(ledger.formattedPending, '₹ 45.67');
    expect(ledger.formattedLifetime, '₹ 9876.54');
  });

  test('empty creator ledger is explicit rather than fabricated activity', () {
    final ledger = EarningsSummary.fromJson({
      'balance_paise': 0,
      'pending_payout_paise': 0,
      'lifetime_earnings_paise': 0,
      'currency': 'INR',
      'is_frozen': false,
      'has_activity': false,
      'updated_at': null,
    });

    expect(ledger.hasActivity, isFalse);
    expect(ledger.availablePaise, 0);
    expect(ledger.updatedAt, isNull);
  });

  test('malformed ledger does not silently become zero', () {
    expect(
      () => EarningsSummary.fromJson({
        'balance_paise': 0,
        'currency': 'INR',
        'updated_at': '2026-08-12T12:00:00Z',
      }),
      throwsFormatException,
    );
  });

  test(
    'creator analytics deserializes recorded values without multipliers',
    () {
      final analytics = CreatorAnalytics.fromJson({
        'views': 101,
        'likes': 12,
        'comments': 3,
        'shares': 4,
        'followers_gained': 0,
        'daily_stats': [
          {'date': '2026-08-12', 'views': 101},
        ],
        'top_posts': [
          {'post_id': 'post-1', 'views': 101},
        ],
      });

      expect(analytics.views, 101);
      expect(analytics.likes, 12);
      expect(analytics.dailyStats.single.views, 101);
      expect(analytics.topPosts.single['views'], 101);
    },
  );

  test('financial writes are default-off in the client', () {
    expect(Environment.monetizationWritesEnabled, isFalse);
  });
}
