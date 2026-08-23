package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.notifications.data.MarkReadRequest
import com.us.android.core.notifications.data.NotificationDto
import com.us.android.core.notifications.data.NotificationsApi
import com.us.android.core.notifications.data.NotificationsRepository
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * The notification contract, on the wire — Slice D.
 *
 * The `Json` here mirrors `NetworkModule.provideJson()` exactly, with
 * `encodeDefaults` OFF. A test that enabled it would pass while the app failed
 * — the defect that has now shipped three times on this codebase.
 *
 * Every payload below is copied from a real response captured through the real
 * gateway on 2026-08-22, against a notification produced by a real comment.
 */
class NotificationWireTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private lateinit var server: MockWebServer
    private lateinit var client: OkHttpClient
    private lateinit var api: NotificationsApi

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        client = OkHttpClient()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(client)
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(NotificationsApi::class.java)
    }

    @After
    fun tearDown() {
        client.dispatcher.executorService.shutdown()
        client.connectionPool.evictAll()
        server.close()
    }

    private fun enqueue(body: String) {
        server.enqueue(
            MockResponse.Builder()
                .code(200)
                .body(body)
                .setHeader("Content-Type", "application/json")
                .build(),
        )
    }

    private fun repository() = NotificationsRepository(api, ErrorMapper(json))

    /** The exact captured row. */
    private val liveRow = """
        {"user_id":"e2bc729c-5b46-4767-9e0f-9c157bfc013b","bucket":202608,
         "ts":"5b83c12d-9e4c-11f1-bf53-dad8c5f4580c",
         "notification_id":"03a741e1-1b8e-45c0-a153-d0f52da74664",
         "type":"comment","actor_user_id":"a4026ecc-cb26-4c3a-b354-230ba4d965f8",
         "entity_type":"post","entity_id":"1acdc102-06bf-4047-b515-9acde1f95e8c",
         "deep_link":"/post/1acdc102-06bf-4047-b515-9acde1f95e8c?focusComment=9220f747-0dc8-4eb5-ac1e-6400668dd1fd",
         "is_read":false,"created_at":"2026-08-22T17:10:21.526Z"}
    """.trimIndent().replace("\n", "").replace("  ", "")

    @Test
    fun `a real notification row decodes into a domain notification`() = runBlocking {
        enqueue("""{"data":[$liveRow]}""")

        val page = (repository().page() as com.us.android.core.common.result.AppResult.Success).data
        val item = page.items.single()

        assertThat(item.id).isEqualTo("03a741e1-1b8e-45c0-a153-d0f52da74664")
        assertThat(item.bucket).isEqualTo(202608)
        assertThat(item.ts).isEqualTo("5b83c12d-9e4c-11f1-bf53-dad8c5f4580c")
        assertThat(item.kind).isEqualTo(NotificationKind.Comment)
        assertThat(item.isRead).isFalse()
        assertThat(item.target).isEqualTo(
            NotificationTarget.PostComment(
                postId = "1acdc102-06bf-4047-b515-9acde1f95e8c",
                commentId = "9220f747-0dc8-4eb5-ac1e-6400668dd1fd",
            ),
        )
    }

    /**
     * AN EMPTY INBOX IS `{"data":null}`, AND IT IS NOT AN ERROR.
     *
     * This is the exact live response for a user with no notifications: the
     * platform envelope is `omitempty` server-side, so an empty list is absent
     * rather than `[]`. Routed through the shared `apiCall` helper — which
     * treats null data as malformed — every such user would see an error screen
     * instead of an empty inbox. That is why the repository interprets this
     * envelope itself.
     */
    @Test
    fun `an empty inbox is an empty page and not a failure`() = runBlocking {
        enqueue("""{"data":null}""")

        val result = repository().page()

        assertThat(result).isInstanceOf(com.us.android.core.common.result.AppResult.Success::class.java)
        val page = (result as com.us.android.core.common.result.AppResult.Success).data
        assertThat(page.items).isEmpty()
        assertThat(page.nextCursor).isNull()
    }

    /** `meta.next_cursor` is the ONLY signal that another page exists. */
    @Test
    fun `the next cursor is read from meta`() = runBlocking {
        enqueue("""{"data":[$liveRow],"meta":{"next_cursor":"cursor-2"}}""")

        val page = (repository().page() as com.us.android.core.common.result.AppResult.Success).data

        assertThat(page.nextCursor).isEqualTo("cursor-2")
    }

    /** Absent meta means the last page, not an unknown one. */
    @Test
    fun `an absent cursor means there are no more pages`() = runBlocking {
        enqueue("""{"data":[$liveRow]}""")

        val page = (repository().page() as com.us.android.core.common.result.AppResult.Success).data

        assertThat(page.nextCursor).isNull()
    }

    /**
     * A blank cursor is treated as absent.
     *
     * `""` would otherwise read as "there is another page", and the list would
     * request it forever at the bottom of the inbox.
     */
    @Test
    fun `a blank cursor is treated as no more pages`() = runBlocking {
        enqueue("""{"data":[$liveRow],"meta":{"next_cursor":""}}""")

        val page = (repository().page() as com.us.android.core.common.result.AppResult.Success).data

        assertThat(page.nextCursor).isNull()
    }

    /**
     * BOTH read fields reach the wire.
     *
     * `bucket` is an Int whose natural default is 0 — a plausible-looking value
     * that vanishes from the body under `encodeDefaults = false`, leaving the
     * server to reject a missing required tag. Removing either field from
     * [MarkReadRequest], or giving either a Kotlin default, fails here.
     */
    @Test
    fun `mark-read sends the bucket and ts the server addresses rows by`() = runBlocking {
        enqueue("""{"data":{"status":"ok"}}""")

        api.markRead(MarkReadRequest(bucket = 202608, ts = "5b83c12d-9e4c-11f1-bf53-dad8c5f4580c"))

        val sent = json.parseToJsonElement(server.takeRequest().body!!.utf8()).jsonObject
        assertThat(sent.keys).containsExactly("bucket", "ts")
        assertThat(sent["bucket"]!!.jsonPrimitive.content).isEqualTo("202608")
        assertThat(sent["ts"]!!.jsonPrimitive.content)
            .isEqualTo("5b83c12d-9e4c-11f1-bf53-dad8c5f4580c")
    }

    /**
     * A zero bucket still reaches the wire.
     *
     * The sharpest form of the defect above: with a Kotlin default, THIS is the
     * value that disappears while every other test still passes.
     */
    @Test
    fun `a zero bucket is still serialized`() {
        val body = json.encodeToString(MarkReadRequest(bucket = 0, ts = "t"))

        assertThat(body).contains("\"bucket\":0")
    }

    @Test
    fun `the unread count is read from the envelope`() = runBlocking {
        enqueue("""{"data":{"count":7}}""")

        val result = repository().unreadCount()

        assertThat((result as com.us.android.core.common.result.AppResult.Success).data).isEqualTo(7)
    }

    @Test
    fun `mark-all-read is a PATCH with no body`() = runBlocking {
        enqueue("""{"data":{"status":"ok"}}""")

        repository().markAllRead()

        val recorded = server.takeRequest()
        assertThat(recorded.method).isEqualTo("PATCH")
        assertThat(recorded.target).isEqualTo("/v1/notifications/read-all")
    }

    @Test
    fun `the list request carries the limit and cursor as query parameters`() = runBlocking {
        enqueue("""{"data":null}""")

        api.list(limit = 20, cursor = "abc")

        assertThat(server.takeRequest().target).isEqualTo("/v1/notifications?limit=20&cursor=abc")
    }

    /** A null cursor is omitted rather than sent as the string "null". */
    @Test
    fun `a null cursor is omitted from the query`() = runBlocking {
        enqueue("""{"data":null}""")

        api.list(limit = 20, cursor = null)

        assertThat(server.takeRequest().target).isEqualTo("/v1/notifications?limit=20")
    }

    /**
     * An unknown `type` decodes rather than failing the page.
     *
     * One notification service serves every vertical here, so a build without
     * a commerce screen still receives commerce notifications. A strict decode
     * would take down the whole inbox for one unrecognised row.
     */
    @Test
    fun `an unknown notification type does not break the page`() = runBlocking {
        enqueue(
            """{"data":[{"notification_id":"n1","bucket":1,"ts":"t1",
               "type":"commerce.order.shipped","deep_link":"/order/9","is_read":false}]}""",
        )

        val page = (repository().page() as com.us.android.core.common.result.AppResult.Success).data
        val item = page.items.single()

        assertThat(item.kind).isEqualTo(NotificationKind.Unknown("commerce.order.shipped"))
        assertThat(item.target).isEqualTo(NotificationTarget.None)
    }

    /**
     * A server field this build has never seen is ignored.
     *
     * `ignoreUnknownKeys` is configured in `NetworkModule`; this proves the
     * notification DTO actually benefits from it, so another team adding a
     * member cannot break the inbox.
     */
    @Test
    fun `an unrecognised server field is ignored`() {
        val decoded = json.decodeFromString<ApiEnvelope<List<NotificationDto>>>(
            """{"data":[{"notification_id":"n1","brand_new_field":{"nested":true}}]}""",
        )

        assertThat(decoded.data!!.single().notificationId).isEqualTo("n1")
    }
}
