// Generic in-process telemetry buffer. Relocated to atpost_network
// (it flushes via ApiClient); re-exported here so existing
// `package:commerce_domain/commerce_telemetry.dart` importers keep working.
export 'package:atpost_network/telemetry.dart';
