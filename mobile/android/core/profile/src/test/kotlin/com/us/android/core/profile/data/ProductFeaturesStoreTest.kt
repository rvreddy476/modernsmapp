package com.us.android.core.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FEATURES_REFRESH_MS
import com.us.android.core.model.Product
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.io.IOException

/**
 * `GET /v1/features` (api-gateway cmd/server/features.go, founder decision C,
 * 24 Oct 2026): strict parsing, the last good answer kept, read once per
 * session and again ten minutes on.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ProductFeaturesStoreTest {

    private val json = Json { ignoreUnknownKeys = true }

    /** The gateway's own body: featuresBody marshals the map, keys sorted (prod: every product closed). */
    private val prodBody =
        """{"data":{"products":{"broadcast_channels":{"enabled":false},"communities":{"enabled":false},""" +
            """"dating":{"enabled":false},"doorstep":{"enabled":false},"food":{"enabled":false},"groups":{"enabled":false},""" +
            """"memories":{"enabled":false},"pages":{"enabled":false},"qa":{"enabled":false},"rider":{"enabled":false}}}}"""

    private fun parse(body: String) = parseProductFeatures(json.parseToJsonElement(body))

    @Test
    fun `the gateway's all-closed body reads every product closed`() {
        assertThat(parse(prodBody)).isEmpty()
    }

    @Test
    fun `only the JSON boolean true opens a product`() {
        val open = parse(
            """{"data":{"products":{"broadcast_channels":{"enabled":true},"food":{"enabled":"true"},""" +
                """"rider":{"enabled":1},"dating":{"enabled":null},"doorstep":true,"qa":{},"pages":{"enabled":false},""" +
                """"shop":{"enabled":true}}}}""",
        )
        // Missing (communities, groups, memories), "true", 1, null, a bare true, {} and false are all
        // closed; a key this build does not know is ignored.
        assertThat(open).containsExactly(Product.BROADCAST_CHANNELS)
    }

    @Test
    fun `a body that is not the features document is no answer`() {
        assertThat(parse("""{"data":{}}""")).isNull()
        assertThat(parse("""{"data":{"products":[]}}""")).isNull()
        assertThat(parse("""{"products":{"qa":{"enabled":true}}}""")).isNull()
        assertThat(parse("""[]""")).isNull()
        assertThat(parse("""{"error":{"code":"INTERNAL_ERROR"}}""")).isNull()
    }

    private class Harness(scope: TestScope) {
        var now = 1_000_000L
        val answers = ArrayDeque<() -> JsonElement>()
        var reads = 0
        val store = ProductFeaturesStore(
            source = {
                reads++
                val next = answers.removeFirstOrNull() ?: throw IOException("offline")
                next()
            },
            scope = scope.backgroundScope,
            clock = { now },
        )
    }

    private fun body(vararg open: String): () -> JsonElement = {
        val entries = open.joinToString(",") { "\"$it\":{\"enabled\":true}" }
        Json.parseToJsonElement("{\"data\":{\"products\":{$entries}}}")
    }

    @Test
    fun `before any answer the built-in default holds`() = runTest(UnconfinedTestDispatcher()) {
        val h = Harness(this)

        assertThat(h.store.open.value).isNull()
        assertThat(h.store.isOpen(Product.BROADCAST_CHANNELS)).isFalse()
        assertThat(h.store.isOpen(Product.COMMUNITIES)).isFalse()
        assertThat(h.store.isOpen(Product.DATING)).isTrue()
    }

    @Test
    fun `one read per session, again only ten minutes on`() = runTest(UnconfinedTestDispatcher()) {
        val h = Harness(this)
        h.answers += body("broadcast_channels")
        h.answers += body()

        h.store.refreshIfStale()
        advanceUntilIdle()
        assertThat(h.store.open.value).containsExactly(Product.BROADCAST_CHANNELS)
        assertThat(h.store.isOpen(Product.BROADCAST_CHANNELS)).isTrue()
        assertThat(h.store.isOpen(Product.DATING)).isFalse()

        h.now += FEATURES_REFRESH_MS - 1
        h.store.refreshIfStale()
        advanceUntilIdle()
        assertThat(h.reads).isEqualTo(1)

        h.now += 1
        h.store.refreshIfStale()
        advanceUntilIdle()
        assertThat(h.reads).isEqualTo(2)
        assertThat(h.store.open.value).isEmpty()
    }

    @Test
    fun `a failed or garbled read keeps the last good answer`() = runTest(UnconfinedTestDispatcher()) {
        val h = Harness(this)
        h.answers += body("food")
        h.answers += { throw IOException("offline") }
        h.answers += { Json.parseToJsonElement("""{"data":{}}""") }

        h.store.refreshIfStale()
        advanceUntilIdle()
        repeat(2) {
            h.now += FEATURES_REFRESH_MS
            h.store.refreshIfStale()
            advanceUntilIdle()
        }

        assertThat(h.reads).isEqualTo(3)
        assertThat(h.store.open.value).containsExactly(Product.FOOD)
    }

    @Test
    fun `a failed first read leaves the built-in default`() = runTest(UnconfinedTestDispatcher()) {
        val h = Harness(this)

        h.store.refreshIfStale()
        advanceUntilIdle()

        assertThat(h.reads).isEqualTo(1)
        assertThat(h.store.open.value).isNull()
        assertThat(h.store.isOpen(Product.BROADCAST_CHANNELS)).isFalse()
    }

    @Test
    fun `a read on its way is not started twice, and a deep link waits for it`() = runTest(UnconfinedTestDispatcher()) {
        val gate = CompletableDeferred<JsonElement>()
        var reads = 0
        val store = ProductFeaturesStore(
            source = {
                reads++
                gate.await()
            },
            scope = backgroundScope,
            clock = { 0L },
        )

        store.refreshIfStale()
        store.refreshIfStale()
        assertThat(reads).isEqualTo(1)

        val waited = CompletableDeferred<Unit>()
        backgroundScope.launch {
            store.awaitFirstRead()
            waited.complete(Unit)
        }
        assertThat(waited.isCompleted).isFalse()
        gate.complete(body("dating")())
        advanceUntilIdle()

        assertThat(waited.isCompleted).isTrue()
        assertThat(store.isOpen(Product.DATING)).isTrue()
    }

    @Test
    fun `the route is a plain GET of v1 features, read as JSON`() = runTest {
        val server = MockWebServer()
        try {
            server.enqueue(
                MockResponse.Builder().code(200).setHeader("Content-Type", "application/json").body(prodBody).build(),
            )
            server.start()
            val api = Retrofit.Builder()
                .baseUrl(server.url("/"))
                .client(OkHttpClient())
                .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
                .build()
                .create(FeaturesApi::class.java)

            val parsed = parseProductFeatures(api.features())

            assertThat(parsed).isEmpty()
            val request = server.takeRequest()
            assertThat(request.method).isEqualTo("GET")
            assertThat(request.url.encodedPath).isEqualTo("/v1/features")
        } finally {
            server.close()
        }
    }

    @Test
    fun `a closed product's notification switches are not shown`() {
        val all = NotificationCategory.entries.toList()
        val withNoAnswer = openCategories(all, null)
        assertThat(withNoAnswer).containsNoneOf(
            NotificationCategory.CHANNEL_UPDATES, NotificationCategory.CHANNEL_URGENT,
            NotificationCategory.COMMUNITY_POSTS, NotificationCategory.COMMUNITY_MENTIONS,
        )
        assertThat(withNoAnswer).containsAtLeast(NotificationCategory.GROUP_POSTS, NotificationCategory.LIKES)

        val allClosed = openCategories(all, emptySet())
        assertThat(allClosed).containsNoneOf(NotificationCategory.GROUP_POSTS, NotificationCategory.GROUP_MENTIONS)
        assertThat(allClosed).contains(NotificationCategory.MESSAGES)

        val channelsOpen = openCategories(all, setOf(Product.BROADCAST_CHANNELS))
        assertThat(channelsOpen).containsAtLeast(NotificationCategory.CHANNEL_UPDATES, NotificationCategory.CHANNEL_URGENT)
    }
}
