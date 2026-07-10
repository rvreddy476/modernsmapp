import 'dart:convert';
import 'dart:typed_data';

import 'package:atpost_network/ssl_pinning.dart';
import 'package:flutter_test/flutter_test.dart';

/// DER TLV with automatic short/long-form length encoding (up to 0xFFFF).
Uint8List _tlv(int tag, List<int> content) {
  final len = content.length;
  if (len < 0x80) {
    return Uint8List.fromList([tag, len, ...content]);
  }
  if (len <= 0xFF) {
    return Uint8List.fromList([tag, 0x81, len, ...content]);
  }
  return Uint8List.fromList([tag, 0x82, len >> 8, len & 0xFF, ...content]);
}

/// Builds a structurally valid (if cryptographically meaningless) DER
/// certificate around [spki], mirroring RFC 5280 field order.
Uint8List _fakeCert(Uint8List spki, {bool withVersion = true}) {
  final tbsContent = <int>[
    if (withVersion) ..._tlv(0xA0, _tlv(0x02, [0x02])), // [0] version v3
    ..._tlv(0x02, [0x01]), // serialNumber
    ..._tlv(0x30, []), // signature algorithm
    ..._tlv(0x30, List.filled(200, 0x00)), // issuer (long-form length)
    ..._tlv(0x30, []), // validity
    ..._tlv(0x30, []), // subject
    ...spki,
  ];
  final certContent = <int>[
    ..._tlv(0x30, tbsContent), // tbsCertificate
    ..._tlv(0x30, []), // signatureAlgorithm
    ..._tlv(0x03, [0x00]), // signatureValue BIT STRING
  ];
  return _tlv(0x30, certContent);
}

void main() {
  group('extractSpki', () {
    final spki = _tlv(0x30, [0x06, 0x03, 0x2A, 0x03, 0x04, 0x05]);

    test('locates the SubjectPublicKeyInfo TLV in a v3 certificate', () {
      expect(extractSpki(_fakeCert(spki)), equals(spki));
    });

    test('handles certificates without the optional version field', () {
      expect(extractSpki(_fakeCert(spki, withVersion: false)), equals(spki));
    });

    test('throws on structures that are not certificates', () {
      expect(
        () => extractSpki(Uint8List.fromList([0x02, 0x01, 0x01])),
        throwsFormatException,
      );
      expect(
        () => extractSpki(Uint8List.fromList([0x30, 0x82])),
        throwsFormatException,
      );
    });
  });

  group('parseSpkiPins', () {
    const hexPin =
        'aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899';

    test('accepts hex, colon-separated hex, and base64 pins', () {
      final colonized = List.generate(
        32,
        (i) => hexPin.substring(i * 2, i * 2 + 2).toUpperCase(),
      ).join(':');
      final b64 = base64.encode(
        List.generate(
          32,
          (i) => int.parse(hexPin.substring(i * 2, i * 2 + 2), radix: 16),
        ),
      );

      expect(parseSpkiPins(hexPin), {hexPin});
      expect(parseSpkiPins(colonized), {hexPin});
      expect(parseSpkiPins(b64), {hexPin});
    });

    test('collects multiple comma-separated pins (primary + backup)', () {
      final other = '00' * 32;
      expect(parseSpkiPins('$hexPin, $other'), {hexPin, other});
    });

    test('skips malformed entries instead of weakening the pin set', () {
      expect(parseSpkiPins('not-a-pin,$hexPin,deadbeef'), {hexPin});
      expect(parseSpkiPins(''), isEmpty);
    });
  });
}
