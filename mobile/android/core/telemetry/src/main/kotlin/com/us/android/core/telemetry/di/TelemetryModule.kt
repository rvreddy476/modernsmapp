package com.us.android.core.telemetry.di

import com.us.android.core.telemetry.NoOpTelemetry
import com.us.android.core.telemetry.OtelTelemetry
import com.us.android.core.telemetry.Telemetry
import com.us.android.core.telemetry.TelemetryConfig
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import io.opentelemetry.api.common.Attributes
import io.opentelemetry.exporter.otlp.http.metrics.OtlpHttpMetricExporter
import io.opentelemetry.exporter.otlp.http.trace.OtlpHttpSpanExporter
import io.opentelemetry.sdk.OpenTelemetrySdk
import io.opentelemetry.sdk.metrics.SdkMeterProvider
import io.opentelemetry.sdk.metrics.export.PeriodicMetricReader
import io.opentelemetry.sdk.resources.Resource
import io.opentelemetry.sdk.trace.SdkTracerProvider
import io.opentelemetry.sdk.trace.export.BatchSpanProcessor
import io.opentelemetry.sdk.trace.samplers.Sampler
import java.time.Duration
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
object TelemetryModule {

    @Provides
    @Singleton
    fun provideTelemetry(config: TelemetryConfig): Telemetry {
        // No endpoint means no sink. Buffering spans that will never be
        // flushed costs memory and battery for nothing.
        if (!config.isEnabled) return NoOpTelemetry

        val resource = Resource.getDefault().merge(
            Resource.create(
                Attributes.builder()
                    .put("service.name", SERVICE_NAME)
                    .put("service.version", config.serviceVersion)
                    .put("deployment.environment", config.environment)
                    .build(),
            ),
        )

        val tracerProvider = SdkTracerProvider.builder()
            .setResource(resource)
            .setSampler(Sampler.traceIdRatioBased(config.traceSampleRatio))
            .addSpanProcessor(
                // Batched, never synchronous: a span export on the calling
                // thread would put network latency inside the operation being
                // measured, which is both wrong and slow.
                BatchSpanProcessor.builder(
                    OtlpHttpSpanExporter.builder()
                        .setEndpoint("${config.otlpEndpoint.trimEnd('/')}/v1/traces")
                        .setTimeout(Duration.ofSeconds(EXPORT_TIMEOUT_SECONDS))
                        .build(),
                )
                    .setScheduleDelay(Duration.ofSeconds(EXPORT_INTERVAL_SECONDS))
                    .setMaxQueueSize(MAX_QUEUE)
                    .build(),
            )
            .build()

        val meterProvider = SdkMeterProvider.builder()
            .setResource(resource)
            .registerMetricReader(
                PeriodicMetricReader.builder(
                    OtlpHttpMetricExporter.builder()
                        .setEndpoint("${config.otlpEndpoint.trimEnd('/')}/v1/metrics")
                        .setTimeout(Duration.ofSeconds(EXPORT_TIMEOUT_SECONDS))
                        .build(),
                )
                    .setInterval(Duration.ofSeconds(METRIC_INTERVAL_SECONDS))
                    .build(),
            )
            .build()

        val sdk = OpenTelemetrySdk.builder()
            .setTracerProvider(tracerProvider)
            .setMeterProvider(meterProvider)
            .build()

        return OtelTelemetry(sdk, config.environment)
    }

    private const val SERVICE_NAME = "us-android"
    private const val EXPORT_TIMEOUT_SECONDS = 10L
    private const val EXPORT_INTERVAL_SECONDS = 5L
    private const val METRIC_INTERVAL_SECONDS = 30L

    // Bounded so a long offline stretch drops spans instead of the process.
    private const val MAX_QUEUE = 512
}
