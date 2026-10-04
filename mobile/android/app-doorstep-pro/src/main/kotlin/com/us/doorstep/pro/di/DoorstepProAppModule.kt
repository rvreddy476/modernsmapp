package com.us.doorstep.pro.di

import com.us.android.core.network.ApiConfig
import com.us.android.core.notifications.IncomingCallPushHandler
import com.us.android.core.notifications.data.PushApp
import com.us.android.core.telemetry.TelemetryConfig
import com.us.android.feature.doorsteppro.deeplink.ProLinkConfig
import com.us.doorstep.pro.BuildConfig
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

/**
 * The only place Doorstep Pro reads BuildConfig — the same rule as :app's
 * AppModule and the partner apps' modules.
 */
@Module
@InstallIn(SingletonComponent::class)
object DoorstepProAppModule {

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

    /** A professional has no calls. :core:notifications' FCM service needs this to compile into the graph. */
    @Provides
    @Singleton
    fun provideIncomingCallPushHandler(): IncomingCallPushHandler = IncomingCallPushHandler { _, _ -> }

    /** Push devices register as `doorstep_pro`, so notification-service routes doorstep.pro.* here and nothing of Momentum's. */
    @Provides
    fun providePushApp(): PushApp = PushApp.DOORSTEP_PRO

    /** The dev flavour alone accepts the custom-scheme DigiLocker return (any app can claim a custom scheme). */
    @Provides
    fun provideProLinkConfig(): ProLinkConfig = ProLinkConfig(allowCustomSchemeDigiLocker = BuildConfig.FLAVOR == DEV_FLAVOR)

    private const val PROD_TRACE_SAMPLE_RATIO = 0.05
    private const val DEV_FLAVOR = "dev"
}
