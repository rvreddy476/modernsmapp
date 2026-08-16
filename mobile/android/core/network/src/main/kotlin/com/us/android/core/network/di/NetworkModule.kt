package com.us.android.core.network.di

import com.us.android.core.network.ApiConfig
import com.us.android.core.network.TokenAuthenticator
import com.us.android.core.network.cookie.CsrfCookieStore
import com.us.android.core.network.interceptor.AuthInterceptor
import com.us.android.core.network.interceptor.ClientHeadersInterceptor
import com.us.android.core.network.interceptor.CsrfInterceptor
import com.us.android.core.network.interceptor.TracingInterceptor
import com.us.android.core.network.retry.RetryConfig
import com.us.android.core.network.retry.RetryInterceptor
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.util.concurrent.TimeUnit
import javax.inject.Qualifier
import javax.inject.Singleton

/**
 * The authenticated client — bearer token, CSRF, standard headers, 401 refresh.
 * Everything that talks to our own API uses this.
 */
@Qualifier
@Retention(AnnotationRetention.BINARY)
annotation class AuthenticatedClient

/**
 * A client carrying NO auth, NO CSRF and NO app headers.
 *
 * Required for S3/GCS presigned uploads: a stray `Authorization` header breaks
 * presigned-URL signature validation outright. Phase 5's media upload
 * (`init` → presigned PUT → `confirm`) depends on this existing, which is why
 * it is declared now rather than discovered later. See PHASE_0_1_PLAN §D.5.
 */
@Qualifier
@Retention(AnnotationRetention.BINARY)
annotation class BareClient

@Module
@InstallIn(SingletonComponent::class)
object NetworkModule {

    @Provides
    @Singleton
    fun provideJson(): Json = Json {
        // The backend adds fields without warning; a client that hard-fails on
        // an unknown key turns every server-side feature launch into an outage.
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    /**
     * Retry policy for the authenticated client.
     *
     * Defaults are deliberately conservative: 3 attempts inside a 10s budget,
     * which must stay under the OkHttp callTimeout or the call is killed
     * mid-backoff and the budget means nothing.
     */
    @Provides
    @Singleton
    fun provideRetryInterceptor(): RetryInterceptor = RetryInterceptor(RetryConfig())

    @Provides
    @Singleton
    fun provideLoggingInterceptor(config: ApiConfig): HttpLoggingInterceptor =
        HttpLoggingInterceptor().apply {
            // BASIC even in debug: HEADERS would print the bearer token and
            // Set-Cookie, BODY would print login passwords. Device logs are
            // readable by other tooling — the Flutter client made the same
            // call for the same reason (api_client.dart:52).
            level = if (config.isDebug) {
                HttpLoggingInterceptor.Level.BASIC
            } else {
                HttpLoggingInterceptor.Level.NONE
            }
        }

    @Provides
    @Singleton
    @BareClient
    fun provideBareOkHttpClient(): OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(CONNECT_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        // Long write window: this client exists to push multi-megabyte media
        // parts at whatever the user's uplink manages.
        .writeTimeout(UPLOAD_WRITE_TIMEOUT_MINUTES, TimeUnit.MINUTES)
        .readTimeout(READ_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .build()

    @Provides
    @Singleton
    @AuthenticatedClient
    @Suppress("LongParameterList")
    fun provideOkHttpClient(
        clientHeadersInterceptor: ClientHeadersInterceptor,
        authInterceptor: AuthInterceptor,
        csrfInterceptor: CsrfInterceptor,
        retryInterceptor: RetryInterceptor,
        tracingInterceptor: TracingInterceptor,
        loggingInterceptor: HttpLoggingInterceptor,
        authenticator: TokenAuthenticator,
        cookieJar: CsrfCookieStore,
    ): OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(CONNECT_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .readTimeout(READ_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .writeTimeout(READ_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        // Total ceiling for one logical call INCLUDING retries and backoff.
        // Without it the socket timeouts bound each attempt but nothing bounds
        // the sequence, so a retrying call can outlive any caller deadline.
        .callTimeout(CALL_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .cookieJar(cookieJar)
        // Retry runs OUTSIDE auth/csrf so a replay re-attaches a fresh bearer
        // and a current CSRF token rather than replaying stale headers.
        .addInterceptor(retryInterceptor)
        // Inside retry, so each ATTEMPT gets its own span and its own RED
        // sample. Measuring only the outermost call would hide the retries
        // that made a request slow.
        .addInterceptor(tracingInterceptor)
        .addInterceptor(clientHeadersInterceptor)
        .addInterceptor(authInterceptor)
        .addInterceptor(csrfInterceptor)
        .addInterceptor(loggingInterceptor)
        .authenticator(authenticator)
        // OFF, deliberately. OkHttp's built-in retry is global: it re-sends
        // non-idempotent writes such as POST /v1/auth/register whose
        // connection dropped mid-flight. Retry is now opt-in per endpoint via
        // RetryInterceptor (quality bar §2: retries only on idempotent
        // operations). The cost is losing automatic route failover for calls
        // that are not retried — which is precisely the set that must not be
        // repeated blindly.
        .retryOnConnectionFailure(false)
        .build()

    @Provides
    @Singleton
    fun provideRetrofit(
        @AuthenticatedClient client: OkHttpClient,
        config: ApiConfig,
        json: Json,
    ): Retrofit = Retrofit.Builder()
        .baseUrl(config.baseUrl.ensureTrailingSlash())
        .client(client)
        .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
        .build()

    private fun String.ensureTrailingSlash(): String =
        if (endsWith("/")) this else "$this/"

    private const val CONNECT_TIMEOUT_SECONDS = 30L
    private const val READ_TIMEOUT_SECONDS = 30L
    private const val UPLOAD_WRITE_TIMEOUT_MINUTES = 5L

    // Must exceed RetryConfig.totalBudgetMillis (10s) with room for the final
    // attempt, or the call is killed mid-retry and the budget is meaningless.
    private const val CALL_TIMEOUT_SECONDS = 45L
}
