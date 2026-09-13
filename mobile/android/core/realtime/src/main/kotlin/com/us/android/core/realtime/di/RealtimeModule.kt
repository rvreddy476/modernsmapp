package com.us.android.core.realtime.di

import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.di.AuthenticatedClient
import com.us.android.core.network.retry.RetryInterceptor
import com.us.android.core.realtime.RealtimeClock
import com.us.android.core.realtime.ReconnectBackoff
import com.us.android.core.realtime.SseClient
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.serialization.json.Json
import okhttp3.Authenticator
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor
import java.util.concurrent.TimeUnit
import javax.inject.Qualifier
import javax.inject.Singleton

/** The streaming client. Only [SseClient] should ask for it. */
@Qualifier
@Retention(AnnotationRetention.BINARY)
annotation class RealtimeHttpClient

@Module
@InstallIn(SingletonComponent::class)
object RealtimeModule {

    @Provides
    @Singleton
    @RealtimeHttpClient
    fun provideRealtimeOkHttpClient(@AuthenticatedClient shared: OkHttpClient): OkHttpClient =
        shared.toRealtimeClient()

    @Provides
    @Singleton
    fun provideSseClient(
        @RealtimeHttpClient client: OkHttpClient,
        config: ApiConfig,
        json: Json,
        @Dispatcher(UsDispatcher.IO) ioDispatcher: CoroutineDispatcher,
    ): SseClient = SseClient(
        client = client,
        baseUrl = config.baseUrl.let { if (it.endsWith("/")) it else "$it/" }.toHttpUrl(),
        json = json,
        backoff = ReconnectBackoff(),
        clock = RealtimeClock.System,
        ioDispatcher = ioDispatcher,
    )
}

/**
 * Derives the streaming client from the shared authenticated one.
 *
 * `newBuilder` shares the connection pool and dispatcher and keeps the header,
 * auth and tracing interceptors. What changes, and why:
 *  - read timeout 60 s, call timeout none: a stream is open for as long as the
 *    screen is, and the server's 25 s keep-alive comment is what proves the
 *    socket alive, so two missed keep-alives is a dead connection;
 *  - [RetryInterceptor] removed and connection-failure retry off: reconnects
 *    belong to [SseClient]'s backoff, and a hidden retry underneath it would
 *    double every attempt against the per-user open-rate limit;
 *  - the logging interceptor removed: it prints the URL, and the topic token
 *    is a query parameter;
 *  - no authenticator: a 401 here is usually an expired TOPIC token, which the
 *    SSE client refreshes itself. The shared authenticator would refresh the
 *    ACCESS token on it — a wasted round trip that fixes nothing. An expired
 *    access token is still refreshed: the topic-token request goes through
 *    the shared Retrofit client and its authenticator.
 */
internal fun OkHttpClient.toRealtimeClient(): OkHttpClient = newBuilder()
    .apply {
        interceptors().removeAll { it is RetryInterceptor || it is HttpLoggingInterceptor }
        networkInterceptors().removeAll { it is HttpLoggingInterceptor }
    }
    .readTimeout(REALTIME_READ_TIMEOUT_SECONDS, TimeUnit.SECONDS)
    .callTimeout(0, TimeUnit.SECONDS)
    .retryOnConnectionFailure(false)
    .authenticator(Authenticator.NONE)
    .build()

internal const val REALTIME_READ_TIMEOUT_SECONDS = 60L
