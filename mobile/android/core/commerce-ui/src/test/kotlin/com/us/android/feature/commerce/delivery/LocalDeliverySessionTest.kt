package com.us.android.feature.commerce.delivery

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import kotlinx.serialization.json.Json
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * Android lane 6: the delivery routes a buyer calls answer 401 UNAUTHORIZED
 * when the session is gone and 404 DELIVERY_NOT_FOUND when the order has no
 * delivery. A 401 is a session problem: it says "sign in again", and the
 * screen's poll stops asking rather than hammering a route that will only
 * refuse. The reader's own Refresh still tries.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class LocalDeliverySessionTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    /** Answers every call with the next canned (status, body), and counts them. */
    private class Wire(vararg answers: Pair<Int, String>) : Interceptor {
        private val queue = ArrayDeque(answers.toList())
        var calls = 0

        override fun intercept(chain: Interceptor.Chain): Response {
            calls++
            val (code, body) = queue.removeFirstOrNull() ?: (200 to """{"data":[]}""")
            return Response.Builder()
                .request(chain.request())
                .protocol(Protocol.HTTP_1_1)
                .code(code)
                .message("x")
                .body(body.toResponseBody("application/json".toMediaType()))
                .build()
        }
    }

    private class Inline : java.util.concurrent.AbstractExecutorService() {
        override fun execute(command: Runnable) = command.run()
        override fun shutdown() = Unit
        override fun shutdownNow(): MutableList<Runnable> = mutableListOf()
        override fun isShutdown() = false
        override fun isTerminated() = false
        override fun awaitTermination(timeout: Long, unit: java.util.concurrent.TimeUnit) = true
    }

    private fun vm(wire: Wire) = LocalDeliveryViewModel(
        Retrofit.Builder()
            .baseUrl("http://localhost/")
            // Calls run inline, so the test scheduler sees their answers.
            .client(OkHttpClient.Builder().dispatcher(okhttp3.Dispatcher(Inline())).addInterceptor(wire).build())
            .addConverterFactory(Json.asConverterFactory("application/json".toMediaType()))
            .build(),
    )

    private val unauthorized =
        401 to """{"error":{"code":"UNAUTHORIZED","message":"missing or invalid X-User-Id header"}}"""

    @Test
    fun `a 401 says sign in again and stops the poll until the reader refreshes`() = runTest(dispatcher) {
        val wire = Wire(unauthorized, 200 to """{"data":[{"id":"j-1","status":"assigned"}]}""")
        val model = vm(wire)

        model.refresh("o-1", seller = false)
        advanceUntilIdle()
        assertThat(model.state.value.error).isEqualTo(DELIVERY_SIGNED_OUT)
        assertThat(model.state.value.signedOut).isTrue()

        // The screen's poll: nothing is sent.
        model.refresh("o-1", seller = false)
        advanceUntilIdle()
        assertThat(wire.calls).isEqualTo(1)

        // The Refresh button: one try, and a success clears it.
        model.refresh("o-1", seller = false, manual = true)
        advanceUntilIdle()
        assertThat(wire.calls).isEqualTo(2)
        assertThat(model.state.value.signedOut).isFalse()
        assertThat(model.state.value.error).isNull()
        assertThat(model.state.value.jobs).hasSize(1)
    }

    @Test
    fun `the failures read as the reader's words, never codes`() {
        assertThat(localDeliveryFailure(401, "UNAUTHORIZED", "missing or invalid X-User-Id header").message)
            .isEqualTo(DELIVERY_SIGNED_OUT)
        assertThat(localDeliveryFailure(401, null, null).sessionEnded).isTrue()

        val missing = localDeliveryFailure(404, "DELIVERY_NOT_FOUND", "delivery not found")
        assertThat(missing.message).contains("couldn't find this delivery")
        assertThat(missing.sessionEnded).isFalse()

        assertThat(localDeliveryFailure(503, "UPSTREAM", "upstream timeout").message).doesNotContain("upstream")
        assertThat(localDeliveryFailure(409, "BAD_STATE", "That job has moved on.").message)
            .isEqualTo("That job has moved on.")
    }

    @Test
    fun `a dropped connection does not end the session`() {
        val after = LocalDeliveryState().withFailure(java.io.IOException("reset"), "Couldn't check delivery.")
        assertThat(after.signedOut).isFalse()
        assertThat(after.error).isEqualTo("Couldn't check delivery.")
    }
}
