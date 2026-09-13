package com.us.feast.rider.di

import com.us.android.core.network.ApiConfig
import com.us.android.core.notifications.IncomingCallPushHandler
import com.us.android.core.notifications.data.PushApp
import com.us.android.core.telemetry.TelemetryConfig
import com.us.feast.rider.BuildConfig
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

/**
 * The only place Feast Rider reads BuildConfig — the same rule as :app's
 * AppModule and :app-kitchen's KitchenAppModule.
 */
@Module
@InstallIn(SingletonComponent::class)
object RiderAppModule {

    @Provides
    @Singleton
    fun provideApiConfig(): ApiConfig = ApiConfig(
        baseUrl = BuildConfig.API_BASE_URL,
        wsBaseUrl = BuildConfig.WS_BASE_URL,
        clientVersion = BuildConfig.VERSION_NAME,
        environment = BuildConfig.ENVIRONMENT,
        isDebug = BuildConfig.DEBUG,
    )

    @Provides
    @Singleton
    fun provideTelemetryConfig(): TelemetryConfig = TelemetryConfig(
        otlpEndpoint = BuildConfig.OTLP_ENDPOINT,
        environment = BuildConfig.ENVIRONMENT,
        serviceVersion = BuildConfig.VERSION_NAME,
        traceSampleRatio = if (BuildConfig.DEBUG) 1.0 else PROD_TRACE_SAMPLE_RATIO,
    )

    /** A rider has no calls. :core:notifications' FCM service needs this to compile into the graph. */
    @Provides
    @Singleton
    fun provideIncomingCallPushHandler(): IncomingCallPushHandler = IncomingCallPushHandler { _, _ -> }

    /** Push devices register as `feast_rider`, so notification-service routes job offers here and nothing of Momentum's. */
    @Provides
    fun providePushApp(): PushApp = PushApp.FEAST_RIDER

    private const val PROD_TRACE_SAMPLE_RATIO = 0.05
}
