package com.us.android.core.realtime

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.retry.RetryInterceptor
import com.us.android.core.realtime.di.toRealtimeClient
import okhttp3.Authenticator
import okhttp3.Interceptor
import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor
import org.junit.Test
import java.util.concurrent.TimeUnit

class RealtimeClientDerivationTest {

    @Test
    fun `the streaming client keeps the shared headers but drops retry, logging, auth refresh and the call timeout`() {
        val headers = Interceptor { chain -> chain.proceed(chain.request()) }
        val shared = OkHttpClient.Builder()
            .addInterceptor(RetryInterceptor())
            .addInterceptor(headers)
            .addInterceptor(HttpLoggingInterceptor())
            .authenticator { _, _ -> null }
            .readTimeout(30, TimeUnit.SECONDS)
            .callTimeout(45, TimeUnit.SECONDS)
            .build()

        val realtime = shared.toRealtimeClient()

        assertThat(realtime.interceptors).containsExactly(headers)
        assertThat(realtime.authenticator).isSameInstanceAs(Authenticator.NONE)
        assertThat(realtime.readTimeoutMillis).isEqualTo(60_000)
        assertThat(realtime.callTimeoutMillis).isEqualTo(0)
        assertThat(realtime.retryOnConnectionFailure).isFalse()
        assertThat(realtime.connectionPool).isSameInstanceAs(shared.connectionPool)
        // The shared client is untouched.
        assertThat(shared.interceptors).hasSize(3)
    }
}
