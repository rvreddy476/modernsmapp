# Feast Rider (A4, 2026-09-13). No Banuba, so none of Momentum's SDK rules.

# -- Optional hooks of libraries already in the graph ---------------------
# The OTel SDK and OTLP exporter reference the incubator API and the
# autoconfigure SPI, neither of which ships (telemetry is wired explicitly).
-dontwarn io.opentelemetry.api.incubator.**
-dontwarn io.opentelemetry.sdk.autoconfigure.spi.**
