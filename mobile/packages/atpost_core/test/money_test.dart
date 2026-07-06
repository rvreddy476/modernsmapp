import 'package:atpost_core/money.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('formatRupees', () {
    test('zero and sub-rupee', () {
      expect(formatRupees(0), '₹0');
      expect(formatRupees(50), '₹0.50');
      expect(formatRupees(5), '₹0.05');
    });

    test('Indian-style grouping', () {
      expect(formatRupees(100), '₹1');
      expect(formatRupees(123456789), '₹12,34,567.89');
      expect(formatRupees(100000), '₹1,000');
      expect(formatRupees(10000000), '₹1,00,000');
    });

    test('negative values render a leading minus', () {
      expect(formatRupees(-12345), '-₹123.45');
    });

    test('withSymbol: false drops the ₹', () {
      expect(formatRupees(123456789, withSymbol: false), '12,34,567.89');
    });
  });

  group('amountBucket', () {
    test('maps paise to coarse privacy bands', () {
      expect(amountBucket(5000), '0-99'); // ₹50
      expect(amountBucket(20000), '100-499'); // ₹200
      expect(amountBucket(70000), '500-999'); // ₹700
      expect(amountBucket(200000), '1000-4999'); // ₹2000
      expect(amountBucket(500000), '5000-9999'); // ₹5000
      expect(amountBucket(20000000), '100000+'); // ₹2,00,000
    });
  });
}
