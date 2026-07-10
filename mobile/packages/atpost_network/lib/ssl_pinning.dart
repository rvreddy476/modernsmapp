// SPKI (public-key) pinning for the app's Dio instances.
//
// One shared install point so ApiClient and the auth client stay in
// lockstep. Pins are SHA-256 hashes of the server certificate's
// SubjectPublicKeyInfo (SPKI), supplied at build time via
// --dart-define=ATPOST_SSL_PINS as a comma-separated list (hex or
// base64). Pinning the public key — not the leaf certificate — means
// routine certificate renewals with the same key pair keep working,
// and shipping a second (backup) pin lets ops rotate the key without
// bricking installed apps.
//
// Generate a pin from a live endpoint:
//   openssl s_client -connect api.example.com:443 < /dev/null 2>/dev/null \
//     | openssl x509 -pubkey -noout \
//     | openssl pkey -pubin -outform DER \
//     | openssl dgst -sha256 -binary | openssl enc -base64
//
// Enforcement model (dio/dart:io):
//  - badCertificateCallback rejects every chain the platform trust
//    store rejects — a chain-invalid cert never connects, pin or not.
//  - IOHttpClientAdapter.validateCertificate then checks the pin on
//    the platform-validated leaf for EVERY connection (dio raises a
//    DioExceptionType.badCertificate and kills the response when it
//    returns false). This closes the classic gap where
//    badCertificateCallback alone never fires for CA-signed MitM certs.
//
// No-op on web (the browser owns TLS), in debug builds (so local
// proxies like Charles/Fiddler keep working), or when no pin is set.

import 'dart:convert';
import 'dart:io';

import 'package:atpost_core/config/environment.dart';
import 'package:atpost_core/utils/app_logger.dart';
import 'package:crypto/crypto.dart';
import 'package:dio/dio.dart';
import 'package:dio/io.dart';
import 'package:flutter/foundation.dart';

/// Installs a pinning [IOHttpClientAdapter] on [dio].
void configureSslPinning(Dio dio, {String tag = 'SslPinning'}) {
  if (kIsWeb) return;

  final pins = parseSpkiPins(Environment.sslPins, tag: tag);
  if (pins.isEmpty || kDebugMode) return;

  dio.httpClientAdapter = IOHttpClientAdapter(
    createHttpClient: () {
      final client = HttpClient();
      // Fail closed on chain validation: pinning is an AND on top of
      // the platform trust store, never an escape hatch around it.
      client.badCertificateCallback = (cert, host, port) {
        AppLogger.error(
          'TLS chain validation failed for $host:$port. Connection rejected.',
          tag: tag,
        );
        return false;
      };
      return client;
    },
    validateCertificate: (cert, host, port) {
      if (cert == null) {
        // No leaf certificate to check the pin against — fail closed.
        AppLogger.error(
          'SSL PINNING: no peer certificate available for $host:$port. '
          'Connection rejected.',
          tag: tag,
        );
        return false;
      }
      try {
        final spkiHash =
            sha256.convert(extractSpki(Uint8List.fromList(cert.der))).toString();
        if (pins.contains(spkiHash)) return true;
        AppLogger.error(
          'SSL PINNING VIOLATION: $host:$port presented SPKI hash $spkiHash '
          'which matches none of the ${pins.length} configured pin(s). '
          'Connection rejected.',
          tag: tag,
        );
        return false;
      } on FormatException catch (e) {
        AppLogger.error(
          'SSL PINNING: could not parse peer certificate for $host:$port '
          '($e). Connection rejected.',
          tag: tag,
        );
        return false;
      }
    },
  );
}

/// Parses a comma-separated pin list into normalized lowercase-hex
/// SHA-256 digests. Each entry may be 64 hex chars (colons allowed,
/// e.g. openssl `AB:CD:...` output) or standard base64 of the 32-byte
/// digest. Malformed entries are logged and skipped rather than
/// silently weakening or hard-crashing the transport.
Set<String> parseSpkiPins(String raw, {String tag = 'SslPinning'}) {
  final pins = <String>{};
  for (final entry in raw.split(',')) {
    final trimmed = entry.trim();
    if (trimmed.isEmpty) continue;
    final hexCandidate = trimmed.replaceAll(':', '').toLowerCase();
    if (RegExp(r'^[0-9a-f]{64}$').hasMatch(hexCandidate)) {
      pins.add(hexCandidate);
      continue;
    }
    try {
      final bytes = base64.decode(trimmed);
      if (bytes.length == 32) {
        pins.add(
          bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join(),
        );
        continue;
      }
    } on FormatException {
      // Falls through to the log below.
    }
    AppLogger.error(
      'Ignoring malformed SPKI pin "$trimmed" — expected 64 hex chars or '
      'base64 of a 32-byte SHA-256 digest.',
      tag: tag,
    );
  }
  return pins;
}

/// Extracts the DER-encoded SubjectPublicKeyInfo from a DER-encoded
/// X.509 certificate. This is what gets hashed for an SPKI pin
/// (identical to what the openssl recipe above digests).
///
/// Certificate ::= SEQUENCE {
///   tbsCertificate       SEQUENCE {
///     version         [0] EXPLICIT Version OPTIONAL,
///     serialNumber        INTEGER,
///     signature           SEQUENCE,
///     issuer              SEQUENCE,
///     validity            SEQUENCE,
///     subject             SEQUENCE,
///     subjectPublicKeyInfo SEQUENCE,   <-- returned TLV
///     ... },
///   ... }
@visibleForTesting
Uint8List extractSpki(Uint8List der) {
  var offset = _enterSequence(der, 0); // Certificate
  offset = _enterSequence(der, offset); // TBSCertificate

  // Optional [0] EXPLICIT version.
  if (der[offset] == 0xA0) {
    offset = _skipTlv(der, offset);
  }
  offset = _skipTlv(der, offset, expectedTag: 0x02); // serialNumber
  offset = _skipTlv(der, offset, expectedTag: 0x30); // signature
  offset = _skipTlv(der, offset, expectedTag: 0x30); // issuer
  offset = _skipTlv(der, offset, expectedTag: 0x30); // validity
  offset = _skipTlv(der, offset, expectedTag: 0x30); // subject

  final spki = _readHeader(der, offset);
  if (spki.tag != 0x30) {
    throw FormatException(
      'Expected SubjectPublicKeyInfo SEQUENCE, found tag '
      '0x${spki.tag.toRadixString(16)}',
    );
  }
  return Uint8List.sublistView(
    der,
    offset,
    offset + spki.headerLength + spki.contentLength,
  );
}

class _DerHeader {
  final int tag;
  final int headerLength;
  final int contentLength;
  const _DerHeader(this.tag, this.headerLength, this.contentLength);
}

_DerHeader _readHeader(Uint8List der, int offset) {
  if (offset + 2 > der.length) {
    throw const FormatException('Truncated DER structure');
  }
  final tag = der[offset];
  final first = der[offset + 1];
  if (first < 0x80) {
    return _DerHeader(tag, 2, first);
  }
  final lengthBytes = first & 0x7F;
  if (lengthBytes == 0 || lengthBytes > 4 ||
      offset + 2 + lengthBytes > der.length) {
    throw const FormatException('Invalid DER length encoding');
  }
  var length = 0;
  for (var i = 0; i < lengthBytes; i++) {
    length = (length << 8) | der[offset + 2 + i];
  }
  return _DerHeader(tag, 2 + lengthBytes, length);
}

/// Returns the offset of the first byte INSIDE the SEQUENCE at [offset].
int _enterSequence(Uint8List der, int offset) {
  final header = _readHeader(der, offset);
  if (header.tag != 0x30) {
    throw FormatException(
      'Expected SEQUENCE, found tag 0x${header.tag.toRadixString(16)}',
    );
  }
  return offset + header.headerLength;
}

/// Returns the offset of the TLV following the one at [offset].
int _skipTlv(Uint8List der, int offset, {int? expectedTag}) {
  final header = _readHeader(der, offset);
  if (expectedTag != null && header.tag != expectedTag) {
    throw FormatException(
      'Expected tag 0x${expectedTag.toRadixString(16)}, found '
      '0x${header.tag.toRadixString(16)}',
    );
  }
  final next = offset + header.headerLength + header.contentLength;
  if (next > der.length) {
    throw const FormatException('DER element overruns buffer');
  }
  return next;
}
