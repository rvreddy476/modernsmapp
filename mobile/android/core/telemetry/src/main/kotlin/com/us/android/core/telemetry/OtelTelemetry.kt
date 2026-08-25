package com.us.android.core.telemetry

import io.opentelemetry.api.OpenTelemetry
import io.opentelemetry.api.common.AttributeKey
import io.opentelemetry.api.common.Attributes
import io.opentelemetry.api.trace.Span
import io.opentelemetry.api.trace.StatusCode
import io.opentelemetry.api.trace.Tracer
import io.opentelemetry.context.Context

/**
 * OpenTelemetry-backed [Telemetry].
 *
 * The SDK is confined to this file and [TelemetryModule]. Everything else in
 * the app talks to the [Telemetry] interface, so replacing the backend is a
 * one-file change rather than a migration.
 */
internal class OtelTelemetry(
    openTelemetry: OpenTelemetry,
    private val environment: String,
) : Telemetry {

    private val tracer: Tracer = openTelemetry.getTracer(INSTRUMENTATION_SCOPE)
    private val meter = openTelemetry.getMeter(INSTRUMENTATION_SCOPE)

    // RED, as three instruments. Duration is a histogram because p95/p99 is
    // what an alert is written against — an average hides exactly the tail
    // that matters (quality bar §3).
    private val durationHistogram = meter
        .histogramBuilder("client.operation.duration")
        .setUnit("ms")
        .setDescription("Client-observed duration of a logical operation")
        .build()

    private val operationCounter = meter
        .counterBuilder("client.operation.count")
        .setDescription("Client operations by status class")
        .build()

    override fun recordOperation(
        operation: Operation,
        statusClass: StatusClass,
        durationMillis: Long,
    ) {
        // The ONLY dimensions permitted. Both come from closed enums, so this
        // cannot be called with a user-derived value even by accident.
        val dimensions = Attributes.builder()
            .put(KEY_OPERATION, operation.metricName)
            .put(KEY_STATUS_CLASS, statusClass.value)
            .put(KEY_ENVIRONMENT, environment)
            .build()

        durationHistogram.record(durationMillis.toDouble(), dimensions)
        operationCounter.add(1, dimensions)
    }

    override fun recordError(
        event: String,
        cause: Throwable?,
        attributes: Map<String, String>,
    ) {
        // A span, not a metric: ids and free-form detail are safe on a span
        // and would be ruinous as metric dimensions.
        val span = tracer.spanBuilder(event).startSpan()
        try {
            attributes.forEach { (k, v) -> span.setAttribute(k, v) }
            span.setAttribute(KEY_ENVIRONMENT, environment)
            span.setStatus(StatusCode.ERROR, cause?.javaClass?.simpleName ?: event)
            cause?.let { span.recordException(it) }
        } finally {
            span.end()
        }
    }

    override fun traceParentHeader(): String? {
        val spanContext = Span.fromContext(Context.current()).spanContext
        if (!spanContext.isValid) return null
        // W3C trace context: version-traceid-spanid-flags. Formatted here
        // rather than pulled from a propagator so the dependency stays small
        // and the format is visible at the point of use.
        return "00-${spanContext.traceId}-${spanContext.spanId}-${spanContext.traceFlags.asHex()}"
    }

    // Catching Throwable is the point: a span must record whatever failed and
    // then RETHROW it unchanged. Narrowing the catch would silently leave
    // Errors unrecorded — and an OOM or a linkage error is exactly the failure
    // worth having a span for. Nothing is swallowed.
    @Suppress("TooGenericExceptionCaught")
    override fun <T> span(operation: Operation, block: () -> T): T {
        val span = tracer.spanBuilder(operation.metricName).startSpan()
        val started = System.nanoTime()
        var status = StatusClass.Success
        return try {
            span.makeCurrent().use { block() }
        } catch (t: Throwable) {
            status = StatusClass.ServerError
            span.setStatus(StatusCode.ERROR)
            span.recordException(t)
            throw t
        } finally {
            span.end()
            recordOperation(
                operation,
                status,
                (System.nanoTime() - started) / NANOS_PER_MILLI,
            )
        }
    }

    private companion object {
        const val INSTRUMENTATION_SCOPE = "com.us.android"
        const val NANOS_PER_MILLI = 1_000_000L
        val KEY_OPERATION: AttributeKey<String> = AttributeKey.stringKey("operation")
        val KEY_STATUS_CLASS: AttributeKey<String> = AttributeKey.stringKey("status_class")
        val KEY_ENVIRONMENT: AttributeKey<String> = AttributeKey.stringKey("environment")
    }
}
