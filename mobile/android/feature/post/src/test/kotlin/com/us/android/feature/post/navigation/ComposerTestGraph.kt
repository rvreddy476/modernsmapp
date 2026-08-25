package com.us.android.feature.post.navigation

import com.us.android.core.network.ApiConfig
import com.us.android.core.network.TokenProvider
import com.us.android.core.network.TokenRefresher
import com.us.android.core.telemetry.TelemetryConfig
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

/**
 * The application-level bindings `:app` normally supplies — Slice C, C-CLB-2.
 *
 * ## WHY THESE THREE, AND WHY HERE
 *
 * `ComposerDiscardDurabilityTest` builds the real Hilt graph so the composer's
 * ViewModel is created and owned by navigation. That graph reaches through
 * `:core:network`, `:core:media` and `:core:engagement`, all of which expect
 * configuration that only `:app` provides: `ApiConfig`, `TelemetryConfig`, and
 * a `TokenProvider` (bound in `:core:auth`, which `:feature:post` deliberately
 * does not depend on).
 *
 * Supplying them from the TEST source set rather than adding `:core:auth` as a
 * dependency keeps the module graph exactly as it is. `moduleGraphCheck` counts
 * production edges, and a feature reaching for an auth module just to make a
 * test compile would be a real architectural regression bought for nothing.
 *
 * Nothing here is ever called: no request is issued during the journey. These
 * exist so the graph can be CONSTRUCTED, which is the precondition for
 * `hiltViewModel()` resolving at all.
 */
@Module
@InstallIn(SingletonComponent::class)
object ComposerTestGraph {

    /**
     * An address nothing resolves.
     *
     * `.invalid` is reserved by RFC 2606 and guaranteed never to resolve, so a
     * test that accidentally starts issuing requests fails loudly instead of
     * quietly reaching something real.
     */
    @Provides
    @Singleton
    fun provideApiConfig(): ApiConfig = ApiConfig(
        baseUrl = "http://composer-journey.invalid/",
        wsBaseUrl = "ws://composer-journey.invalid",
        clientVersion = "test",
        environment = "test",
        isDebug = true,
    )

    /** Blank endpoint disables export, so the journey records nothing anywhere. */
    @Provides
    @Singleton
    fun provideTelemetryConfig(): TelemetryConfig = TelemetryConfig(
        otlpEndpoint = "",
        environment = "test",
        serviceVersion = "test",
    )

    /** No session. The journey never authenticates and must never need to. */
    @Provides
    @Singleton
    fun provideTokenProvider(): TokenProvider = object : TokenProvider {
        override fun currentAccessToken(): String? = null
    }

    /** Never refreshes: with no session there is nothing to refresh. */
    @Provides
    @Singleton
    fun provideTokenRefresher(): TokenRefresher = object : TokenRefresher {
        override suspend fun refresh(): String? = null
    }
}
