package com.us.android.core.network.interceptor

import com.us.android.core.telemetry.Operation
import com.us.android.core.telemetry.StatusClass
import com.us.android.core.telemetry.Telemetry
import okhttp3.Interceptor
import okhttp3.Response
import java.io.IOException
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Propagates W3C trace context and records RED for every request.
 *
 * The `traceparent` header is what joins a client span to the server trace
 * produced by the Go services' `OtelTracing` middleware. Without it the app
 * and the backend produce two unrelated traces and "where did the latency go"
 * has no answer that crosses the network boundary.
 *
 * Note what is NOT recorded as a metric dimension: no URL, no path, no user
 * id, no request id. A path with an id in it (`/v1/profiles/{uuid}`) is
 * unbounded cardinality — every profile viewed would mint a new time series.
 * The URL goes on the span, where it is free.
 */
@Singleton
class TracingInterceptor @Inject constructor(
    private val telemetry: Telemetry,
) : Interceptor {

    override fun intercept(chain: Interceptor.Chain): Response {
        val builder = chain.request().newBuilder()
        telemetry.traceParentHeader()?.let { builder.header(TRACEPARENT, it) }
        val request = builder.build()

        val started = System.nanoTime()
        return try {
            val response = chain.proceed(request)
            telemetry.recordOperation(
                Operation.HttpRequest,
                response.code.toStatusClass(),
                elapsedMillis(started),
            )
            response
        } catch (e: IOException) {
            telemetry.recordOperation(
                Operation.HttpRequest,
                StatusClass.NetworkError,
                elapsedMillis(started),
            )
            // The URL is an attribute on the error span, never a dimension.
            telemetry.recordError(
                event = "http.request.failed",
                cause = e,
                attributes = mapOf(
                    "http.method" to request.method,
                    "url.path" to request.url.encodedPath,
                ),
            )
            throw e
        }
    }

    private fun elapsedMillis(startNanos: Long) = (System.nanoTime() - startNanos) / NANOS_PER_MILLI

    private fun Int.toStatusClass(): StatusClass = when (this) {
        in SUCCESS_MIN..SUCCESS_MAX -> StatusClass.Success
        in CLIENT_MIN..CLIENT_MAX -> StatusClass.ClientError
        else -> StatusClass.ServerError
    }

    private companion object {
        const val TRACEPARENT = "traceparent"
        const val NANOS_PER_MILLI = 1_000_000L
        const val SUCCESS_MIN = 200
        const val SUCCESS_MAX = 399
        const val CLIENT_MIN = 400
        const val CLIENT_MAX = 499
    }
}
