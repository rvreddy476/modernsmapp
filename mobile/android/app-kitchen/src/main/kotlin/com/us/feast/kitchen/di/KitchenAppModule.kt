package com.us.feast.kitchen.di

import com.us.android.core.network.ApiConfig
import com.us.android.core.notifications.IncomingCallPushHandler
import com.us.android.core.telemetry.TelemetryConfig
import com.us.android.feature.kitchen.queue.NewOrderAlert
import com.us.feast.kitchen.BuildConfig
import com.us.feast.kitchen.alert.KitchenNewOrderAlert
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

/**
 * The only place Feast Kitchen reads BuildConfig — the same rule as :app's
 * AppModule. Everything below receives configuration through DI.
 */
@Module
@InstallIn(SingletonComponent::class)
object KitchenAppModule {

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

    /**
     * The kitchen has no calls. :core:notifications' FCM service needs this
     * binding to compile into the graph (Momentum gets it from :core:call, which
     * the kitchen does not ship); the service itself is removed from the manifest.
     */
    @Provides
    @Singleton
    fun provideIncomingCallPushHandler(): IncomingCallPushHandler = IncomingCallPushHandler { _, _ -> }

    private const val PROD_TRACE_SAMPLE_RATIO = 0.05
}

@Module
@InstallIn(SingletonComponent::class)
abstract class KitchenAlertModule {

    @Binds
    abstract fun bindNewOrderAlert(impl: KitchenNewOrderAlert): NewOrderAlert
}
