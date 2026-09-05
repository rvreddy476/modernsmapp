package com.us.android.feature.post.navigation

import com.us.android.core.common.session.SessionTeardownTask
import com.us.android.core.engagement.data.CommentsViewerSource
import com.us.android.core.network.ApiConfig
import com.us.android.core.telemetry.TelemetryConfig
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import dagger.multibindings.Multibinds
import javax.inject.Singleton

/**
 * The application-level bindings `:app` normally supplies — Slice C, C-CLB-2.
 *
 * ## WHY THESE THREE, AND WHY HERE
 *
 * `ComposerDiscardDurabilityTest` builds the real Hilt graph so the composer's
 * ViewModel is created and owned by navigation. That graph reaches through
 * `:core:network`, `:core:media`, `:core:engagement` and — since the channel
 * gate (2026-09-05) — `:core:feed`, which brings `:core:auth` with it. The
 * auth module now binds the token provider and refresher itself; what is
 * still missing is what only `:app` provides: `ApiConfig`, `TelemetryConfig`,
 * the comments viewer, and the session teardown set that `:app`'s modules
 * contribute to ([ComposerTestMultibinds] declares it empty).
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

    /**
     * Nobody is signed in, so the comments composer has no viewer to draw.
     * The real source lives in `:app` (`ProfileCommentsViewerSource`), which
     * this graph does not include.
     */
    @Provides
    @Singleton
    fun provideCommentsViewerSource(): CommentsViewerSource = CommentsViewerSource { null }
}

/**
 * `AuthRepository` takes the set of session teardown tasks that `:core:chat`,
 * `:core:notifications` and `:app` contribute into. None of them is on this
 * graph, and Dagger refuses an undeclared empty set, so it is declared here.
 */
@Module
@InstallIn(SingletonComponent::class)
abstract class ComposerTestMultibinds {
    @Multibinds
    abstract fun teardownTasks(): Set<SessionTeardownTask>
}
