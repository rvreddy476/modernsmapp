package com.us.android.mopedu.captain.di

import com.us.android.mopedu.captain.BuildConfig
import com.us.android.core.network.ApiConfig
import com.us.android.core.telemetry.TelemetryConfig
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
object CaptainAppModule {

    @Provides
    @Singleton
    fun provideTelemetryConfig(): TelemetryConfig = TelemetryConfig(
        otlpEndpoint = BuildConfig.OTLP_ENDPOINT,
        environment = BuildConfig.ENVIRONMENT,
        serviceVersion = BuildConfig.VERSION_NAME,
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
}
