package com.us.android.di

import com.us.android.BuildConfig
import com.us.android.core.network.ApiConfig
import com.us.android.core.telemetry.TelemetryConfig
import com.us.android.feature.post.createhub.banuba.BanubaConfig
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

/**
 * The only place `BuildConfig` is read.
 *
 * `:app` is the sole flavored module, so it is the only one that can see the
 * per-environment constants. Everything below it receives configuration
 * through DI, which is what keeps each library module at two variants
 * instead of six.
 */
@Module
@InstallIn(SingletonComponent::class)
object AppModule {

    /**
     * Telemetry destination.
     *
     * `OTLP_ENDPOINT` is blank for prod, which yields [NoOpTelemetry]. Aiming
     * a whole mobile fleet at a collector is a capacity and cost decision
     * (audit G1), not a build default.
     */
    @Provides
    @Singleton
    fun provideTelemetryConfig(): TelemetryConfig = TelemetryConfig(
        otlpEndpoint = BuildConfig.OTLP_ENDPOINT,
        environment = BuildConfig.ENVIRONMENT,
        serviceVersion = BuildConfig.VERSION_NAME,
        // 1.0 in dev so a single manual test yields a visible trace.
        // Production must be set from measured volume, not from this default.
        traceSampleRatio = if (BuildConfig.DEBUG) 1.0 else 0.05,
    )

    @Provides
    @Singleton
    fun provideApiConfig(): ApiConfig = ApiConfig(
        baseUrl = BuildConfig.API_BASE_URL,
        wsBaseUrl = BuildConfig.WS_BASE_URL,
        clientVersion = BuildConfig.VERSION_NAME,
        environment = BuildConfig.ENVIRONMENT,
        isDebug = BuildConfig.DEBUG,
    )

    /**
     * Banuba Video Editor licence (2026-09-05). Blank when the build had no
     * `.secrets/banuba.token`, and the reel flow then stays on the Media3
     * studio. The value is never logged; the gate only tests for blank.
     */
    @Provides
    @Singleton
    fun provideBanubaConfig(): BanubaConfig = BanubaConfig(
        licenseToken = BuildConfig.BANUBA_LICENSE_TOKEN,
    )
}
