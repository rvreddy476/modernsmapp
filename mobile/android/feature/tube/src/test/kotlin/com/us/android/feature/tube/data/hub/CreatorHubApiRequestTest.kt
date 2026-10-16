package com.us.android.feature.tube.data.hub

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * T-31: the Creator Hub's four reads, byte for byte against a local server —
 * the same routes and parameters the web hub sends (`hubApi.ts`), through the
 * app's own JSON configuration and the repository that normalises them.
 */
class CreatorHubApiRequestTest {

    private val json = NetworkModule.provideJson()
    private val errorMapper = ErrorMapper(json)
    private lateinit var server: MockWebServer
    private lateinit var repository: CreatorHubRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(CreatorHubApi::class.java)
        repository = CreatorHubRepository(api, errorMapper)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    // ── analytics ──────────────────────────────────────────────────────

    @Test
    fun `insights ask analytics for the chosen period and read the bare stats body`() {
        enqueue("""{"period": "7d", "views": 40, "likes": 14, "comments": 5, "shares": 3, "followers_delta": null}""")

        val result = runBlocking { repository.insights(HubPeriod.DAYS_7) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/analytics/creator/me?period=7d")
        val data = (result as AppResult.Success).data
        assertThat(data.views).isEqualTo(40)
        assertThat(data.likes).isEqualTo(14)
        assertThat(data.followersDelta).isNull()
    }

    @Test
    fun `each period sends its own wire value`() {
        for (period in HubPeriod.entries) {
            enqueue("{}")
            runBlocking { repository.insights(period) }
            assertThat(server.takeRequest().target).isEqualTo("/v1/analytics/creator/me?period=${period.wire}")
        }
    }

    @Test
    fun `an enveloped insights body is unwrapped`() {
        enqueue("""{"data": {"views": 9, "total_likes": 2}}""")

        val data = (runBlocking { repository.insights(HubPeriod.DAYS_28) } as AppResult.Success).data

        assertThat(data.views).isEqualTo(9)
        assertThat(data.likes).isEqualTo(2)
        assertThat(data.period).isEqualTo("28d")
    }

    @Test
    fun `an analytics failure is a failure, not zeros`() {
        enqueue("""{"error": {"code": "INTERNAL_ERROR"}}""", code = 500)

        val result = runBlocking { repository.insights(HubPeriod.DAYS_7) }

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    // ── library ────────────────────────────────────────────────────────

    @Test
    fun `videos are GET uploads videos with the web's page size, rows normalised, the cursor read`() {
        enqueue(
            """{"data": [{"id": "v1", "title": "One", "view_count": 9, "like_count": 4, "comment_count": 2, "visibility": "public"},
                         {"title": "no id"}],
                "meta": {"next_cursor": "c2"}}""",
        )

        val page = (runBlocking { repository.library(HubLibraryKind.VIDEOS) } as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/uploads/videos?limit=30")
        assertThat(page.items.map { it.id }).containsExactly("v1")
        assertThat(page.items[0].likeCount).isEqualTo(4)
        assertThat(page.nextCursor).isEqualTo("c2")
    }

    @Test
    fun `reels are the flicks list, and the next page passes the cursor back untouched`() {
        enqueue("""{"data": [], "meta": {}}""")

        val result = runBlocking { repository.library(HubLibraryKind.FLICKS, cursor = "2026-10-09T00:00:00.123Z") }
        val page = (result as AppResult.Success).data

        assertThat(server.takeRequest().target)
            .isEqualTo("/v1/uploads/flicks?limit=30&cursor=2026-10-09T00%3A00%3A00.123Z")
        assertThat(page.items).isEmpty()
        assertThat(page.nextCursor).isNull()
    }

    @Test
    fun `a nil list (data null) is an empty library, not an error`() {
        enqueue("""{"data": null}""")

        val result = runBlocking { repository.library(HubLibraryKind.VIDEOS) }

        assertThat((result as AppResult.Success).data.items).isEmpty()
    }

    // ── drafts ─────────────────────────────────────────────────────────

    @Test
    fun `drafts are GET reels drafts with limit 20 and the cursor`() {
        enqueue(
            """{"data": [{"id": "d1", "title": "", "caption": "First line\nmore", "status": "draft"},
                         {"id": "", "title": "dropped"},
                         {"id": "d2", "title": "Out", "status": "published", "published_post_id": "p9"}],
                "meta": {"next_cursor": "2026-10-09T00:00:00.123Z"}}""",
        )

        val page = (runBlocking { repository.drafts(cursor = "2026-10-11T00:00:00Z") } as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/reels/drafts?limit=20&cursor=2026-10-11T00%3A00%3A00Z")
        assertThat(page.items.map { it.id }).containsExactly("d1", "d2").inOrder()
        assertThat(page.items[0].title).isEqualTo("First line")
        assertThat(page.nextCursor).isEqualTo("2026-10-09T00:00:00.123Z")
    }

    // ── comments inbox ─────────────────────────────────────────────────

    @Test
    fun `the inbox is every comment of every kind, newest first, 30 a page`() {
        enqueue(
            """{"data": [{"comment": {"id": "c1", "post_id": "p1", "author_id": "u1", "body": "Great one",
                                      "author": {"id": "u1", "username": "asha", "display_name": "Asha"}},
                          "post": {"id": "p1", "title": "One", "content_type": "long_video", "cover_media_id": null},
                          "author_replied": false}],
                "meta": {"next_cursor": "n1"}}""",
        )

        val page = (runBlocking { repository.inbox() } as AppResult.Success).data

        assertThat(server.takeRequest().target)
            .isEqualTo("/v1/comments/inbox?status=all&content=all&sort=newest&limit=30")
        assertThat(page.items.single().body).isEqualTo("Great one")
        assertThat(page.items.single().authorName).isEqualTo("Asha")
        assertThat(page.nextCursor).isEqualTo("n1")
    }

    @Test
    fun `the inbox's next page sends the cursor, and an error envelope is a failure`() {
        enqueue("""{"error": {"code": "INVALID_REQUEST", "message": "bad cursor"}}""", code = 400)

        val result = runBlocking { repository.inbox(cursor = "n1") }

        assertThat(server.takeRequest().target)
            .isEqualTo("/v1/comments/inbox?status=all&content=all&sort=newest&limit=30&cursor=n1")
        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }
}
