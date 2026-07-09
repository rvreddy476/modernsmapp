// Certificate-fingerprint pinning for the app's Dio instances.
//
// One shared install point so ApiClient and the auth client stay in
// lockstep. The pin is the SHA-256 of the server certificate's DER
// bytes, supplied at build time via --dart-define=ATPOST_SSL_FINGERPRINT
// (see Environment.sslFingerprint). Empty pin or debug builds = no-op so
// local proxies (Charles/Fiddler) keep working.
//
// KNOWN LIMITATION (tracked follow-up): badCertificateCallback only fires
// for certs that FAIL default chain validation, so this blocks
// self-signed/proxy MitM but is not full pinning; and pinning the leaf
// cert means a server cert rotation requires an app update. The planned
// hardening is SPKI pinning with a backup pin.

import 'dart:io';

import 'package:atpost_core/config/environment.dart';
import 'package:atpost_core/utils/app_logger.dart';
import 'package:crypto/crypto.dart';
import 'package:dio/dio.dart';
import 'package:dio/io.dart';
import 'package:flutter/foundation.dart';

/// Installs a pinning [IOHttpClientAdapter] on [dio]. No-op on web (the
/// browser owns TLS), in debug builds, or when no fingerprint is pinned.
void configureSslPinning(Dio dio, {String tag = 'SslPinning'}) {
  if (kIsWeb) return;

  final fingerprint =
      Environment.sslFingerprint.replaceAll(':', '').toLowerCase();
  if (fingerprint.isEmpty || kDebugMode) return;

  dio.httpClientAdapter = IOHttpClientAdapter(
    createHttpClient: () {
      final client = HttpClient();
      client.badCertificateCallback = (cert, host, port) {
        // Digest.toString() is lowercase hex, matching the normalized pin.
        final certFingerprint = sha256.convert(cert.der).toString();
        if (certFingerprint == fingerprint) return true;

        AppLogger.error(
          'SSL PINNING VIOLATION: Host $host presented certificate '
          'fingerprint $certFingerprint which does not match the pinned '
          'value. Connection rejected.',
          tag: tag,
        );
        return false;
      };
      return client;
    },
  );
}
