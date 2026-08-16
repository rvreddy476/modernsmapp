package com.us.android.core.telemetry

/**
 * Where telemetry goes, and whether it goes at all.
 *
 * Provided by `:app` from BuildConfig, like [com.us.android.core.network.ApiConfig] —
 * library modules never read BuildConfig directly.
 */
data class TelemetryConfig(
    /**
     * OTLP/HTTP endpoint, e.g. `http://127.0.0.1:4318`.
     *
     * Blank disables export entirely and the app runs on [NoOpTelemetry]. That
     * is the correct default for a build with nowhere to send data: silently
     * buffering spans that will never be flushed costs memory for nothing.
     */
    val otlpEndpoint: String,
    val environment: String,
    val serviceVersion: String,
    /**
     * Head sampling ratio, 0.0–1.0.
     *
     * 1.0 in dev so a single manual test produces a visible trace. Production
     * must be far lower — a mobile fleet at full sampling is a cost incident,
     * and the AWS capacity gate (G1) has to set this from measured volume.
     */
    val traceSampleRatio: Double = 1.0,
) {
    val isEnabled: Boolean get() = otlpEndpoint.isNotBlank()
}
