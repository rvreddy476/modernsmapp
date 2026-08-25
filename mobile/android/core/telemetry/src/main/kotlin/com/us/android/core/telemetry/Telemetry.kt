package com.us.android.core.telemetry

/**
 * The app's only telemetry surface.
 *
 * Features depend on this, never on the OpenTelemetry SDK. That keeps the
 * vendor decision reversible and — more importantly — puts the cardinality
 * rules in one enforceable place instead of trusting every call site.
 *
 * **Cardinality is a hard rule, not a guideline.** Metric dimensions are
 * limited to the closed set in [Operation] and [StatusClass]. A user id,
 * request id, email, post id or content id as a dimension multiplies
 * time-series count by the size of that set and turns a metrics bill into a
 * incident. Those values belong on a *span* or in a *log*, where high
 * cardinality is free — never on a metric.
 */
interface Telemetry {

    /**
     * Records one RED sample: rate, errors, duration.
     *
     * [operation] and [statusClass] are closed enums precisely so this cannot
     * be called with a user-derived string.
     */
    fun recordOperation(
        operation: Operation,
        statusClass: StatusClass,
        durationMillis: Long,
    )

    /**
     * Reports a failure with its cause.
     *
     * [attributes] land on a span, not a metric, so ids and detail are safe
     * here. Never put a secret in them — this leaves the device.
     */
    fun recordError(
        event: String,
        cause: Throwable? = null,
        attributes: Map<String, String> = emptyMap(),
    )

    /**
     * W3C `traceparent` for an outgoing request, or null when tracing is off.
     *
     * This is what lets a client span join the server trace produced by the
     * Go services' `OtelTracing` middleware, giving one trace that spans
     * app → gateway → auth-service.
     */
    fun traceParentHeader(): String?

    /** Runs [block] as a traced span, recording RED on the way out. */
    fun <T> span(operation: Operation, block: () -> T): T
}

/**
 * The closed set of operations that may appear as a metric dimension.
 *
 * Adding a value here is a deliberate act. Anything that would require a
 * dynamic value does not belong on a metric.
 */
enum class Operation(val metricName: String) {
    AuthLogin("auth.login"),
    AuthRegister("auth.register"),
    AuthRefresh("auth.refresh"),
    AuthVerifyEmail("auth.verify_email"),
    AuthResendVerification("auth.resend_verification"),
    AuthLogout("auth.logout"),
    HttpRequest("http.request"),
}

/**
 * Status bucketed to a class, never a raw code.
 *
 * `status_class` has five values; `status_code` has dozens and grows. The
 * bucket is what an alert threshold is written against anyway.
 */
enum class StatusClass(val value: String) {
    Success("success"),
    ClientError("client_error"),
    ServerError("server_error"),
    NetworkError("network_error"),
    Cancelled("cancelled"),
}

/** No-op used in tests and when telemetry is disabled. */
object NoOpTelemetry : Telemetry {
    override fun recordOperation(operation: Operation, statusClass: StatusClass, durationMillis: Long) = Unit
    override fun recordError(event: String, cause: Throwable?, attributes: Map<String, String>) = Unit
    override fun traceParentHeader(): String? = null
    override fun <T> span(operation: Operation, block: () -> T): T = block()
}
