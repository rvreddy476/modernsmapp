// The fixtures at the bottom are response bodies copied VERBATIM from the
// 2026-08-17 repair capture. Reformatting recorded evidence destroys its value.
@file:Suppress("MaxLineLength", "MaximumLineLength")

package com.us.android.feature.feed.data

import androidx.paging.PagingSource
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.model.FeedSurface
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
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
 * Contract tests for the feed, against the 2026-08-17 repair capture
 * (prompt/android-api-contracts.md §1).
 *
 * This shape could not be written on 2026-08-16 — every surface returned
 * `{"data":[]}` and the item DTO had never been observed. These fixtures are
 * the first real ones.
 */
class FeedContractTest {

    private lateinit var server: MockWebServer
    private lateinit var api: FeedApi
    private lateinit var errorMapper: ErrorMapper
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(FeedApi::class.java)
        errorMapper = ErrorMapper(json)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(code: Int, body: String) {
        server.enqueue(
            MockResponse.Builder()
                .code(code)
                .setHeader("Content-Type", "application/json")
                .body(body)
                .build(),
        )
    }

    private fun source(surface: FeedSurface) =
        FeedPagingSource(api, surface, errorMapper)

    private suspend fun load(
        surface: FeedSurface,
        key: String? = null,
    ) = source(surface).load(
        PagingSource.LoadParams.Refresh(key, PAGE_SIZE, false),
    )

    @Test
    fun `captured video item deserializes`() = runTest {
        enqueue(200, HOME_VIDEO_PAGE)

        val page = load(FeedSurface.Home) as PagingSource.LoadResult.Page

        assertThat(page.data).hasSize(1)
        val item = page.data.first()
        assertThat(item.id).isEqualTo("3d752833-089d-48fa-aae2-625fcf602924")
        assertThat(item.postType).isEqualTo("video")
        assertThat(item.feedContentType).isEqualTo("long_video")
        assertThat(item.media).hasSize(1)
        assertThat(item.media.first().kind).isEqualTo("video")
        assertThat(item.media.first().mediaId)
            .isEqualTo("7ee053fc-59aa-4b24-99e9-fdbcace8fa3e")
    }

    @Test
    fun `captured image item deserializes`() = runTest {
        enqueue(200, HOME_IMAGE_PAGE)

        val page = load(FeedSurface.Home) as PagingSource.LoadResult.Page

        val item = page.data.first()
        assertThat(item.postType).isEqualTo("image")
        assertThat(item.feedContentType).isEqualTo("post")
        assertThat(item.media.first().kind).isEqualTo("image")
    }

    /**
     * Home is chronological and carries `meta.next_cursor`. The cursor is an
     * RFC3339 timestamp, passed back opaquely — never parsed, never built.
     */
    @Test
    fun `home yields the captured cursor as the next key`() = runTest {
        enqueue(200, HOME_VIDEO_PAGE)

        val page = load(FeedSurface.Home) as PagingSource.LoadResult.Page

        assertThat(page.nextKey).isEqualTo("2026-08-16T19:44:32.998Z")
        assertThat(page.prevKey).isNull()
    }

    /**
     * THE pagination rule. Reels, videos and watch returned a full page with
     * no `meta` and no cursor at all, and the capture says a cursor must not
     * be invented for them. A source that returned a non-null key here would
     * loop, and one that reused the last key would refetch page one forever.
     */
    @Test
    fun `ranked surfaces terminate because they have no cursor`() = runTest {
        listOf(FeedSurface.Reels, FeedSurface.Videos, FeedSurface.Watch).forEach { surface ->
            enqueue(200, REELS_PAGE)

            val page = load(surface) as PagingSource.LoadResult.Page

            assertThat(page.data).isNotEmpty()
            assertThat(page.nextKey).isNull()
        }
    }

    /** Ranked surfaces carry a score; home does not. Its presence is the signal. */
    @Test
    fun `score is present on ranked surfaces and absent on home`() = runTest {
        enqueue(200, REELS_PAGE)
        val ranked = (load(FeedSurface.Reels) as PagingSource.LoadResult.Page).data.first()
        assertThat(ranked.score).isNotNull()

        enqueue(200, HOME_VIDEO_PAGE)
        val home = (load(FeedSurface.Home) as PagingSource.LoadResult.Page).data.first()
        assertThat(home.score).isNull()
    }

    /** An empty page ends paging even if a cursor were echoed back. */
    @Test
    fun `an empty page terminates`() = runTest {
        enqueue(200, """{"data":[],"meta":{"next_cursor":"2026-08-16T19:44:32.998Z"}}""")

        val page = load(FeedSurface.Home) as PagingSource.LoadResult.Page

        assertThat(page.data).isEmpty()
        assertThat(page.nextKey).isNull()
    }

    @Test
    fun `a text post with no media field deserializes`() = runTest {
        enqueue(200, """{"data":[{"id":"t","author_id":"a","text":"plain","post_type":"text"}]}""")

        val page = load(FeedSurface.Home) as PagingSource.LoadResult.Page

        assertThat(page.data.first().media).isEmpty()
    }

    @Test
    fun `the cursor is sent on the next page request`() = runTest {
        enqueue(200, HOME_VIDEO_PAGE)

        load(FeedSurface.Home, key = "2026-08-16T19:44:32.998Z")

        val request = server.takeRequest()
        assertThat(request.target).contains("cursor=2026-08-16T19%3A44%3A32.998Z")
        assertThat(request.target).startsWith("/v1/feed/home")
    }

    /** No token is a 401; the UI must be able to say "sign in", not "retry". */
    @Test
    fun `an unauthenticated feed surfaces a typed auth error`() = runTest {
        enqueue(401, """{"error":{"code":"UNAUTHORIZED","message":"Invalid user ID"}}""")

        val result = load(FeedSurface.Home) as PagingSource.LoadResult.Error

        val error = (result.throwable as AppErrorException).error
        assertThat(error).isInstanceOf(AppError.AuthFailed::class.java)
    }

    @Test
    fun `unknown item fields are ignored`() = runTest {
        enqueue(200, """{"data":[{"id":"x","author_id":"a","brand_new":{"n":1}}]}""")

        val page = load(FeedSurface.Home) as PagingSource.LoadResult.Page

        assertThat(page.data.first().id).isEqualTo("x")
    }

    private companion object {
        const val PAGE_SIZE = 15

        const val HOME_VIDEO_PAGE =
            """{"data":[{"id":"3d752833-089d-48fa-aae2-625fcf602924","author_id":"719e2958-f412-44ca-b94a-b00060a7fccb","text":"Landscape PostTube — approved contract fixture","visibility":"public","content_type":"long_video","is_pinned":false,"created_at":"2026-08-16T19:44:32.998142Z","updated_at":"2026-08-16T19:44:33.065329Z","media":[{"media_id":"7ee053fc-59aa-4b24-99e9-fdbcace8fa3e","kind":"video"}],"counts":{"likes":0,"comments":0},"view_count":0,"is_bookmarked":false,"post_type":"video","app_origin":"posttube","share_to_postbook":true,"feed_content_type":"long_video"}],"meta":{"next_cursor":"2026-08-16T19:44:32.998Z"}}"""

        const val HOME_IMAGE_PAGE =
            """{"data":[{"id":"a742c9a7-acb9-4751-a5b4-0f0a7b7763c8","author_id":"2d373f48-6d0f-4a62-b439-51dee0b0ec2e","text":"Followed image fixture for mixed feed","visibility":"public","content_type":"post","is_pinned":false,"created_at":"2026-08-16T20:21:02.821823Z","updated_at":"2026-08-16T20:21:02.821823Z","media":[{"media_id":"e4484a71-e26d-423a-a179-9ece7f977f11","kind":"image"}],"counts":{"likes":0,"comments":0},"view_count":0,"is_bookmarked":false,"post_type":"image","app_origin":"postbook","share_to_postbook":true,"feed_content_type":"post"}],"meta":{"next_cursor":"2026-08-16T20:21:02.821Z"}}"""

        const val REELS_PAGE =
            """{"data":[{"id":"724ce232-0a7c-48c3-9875-3f3ccef188b9","author_id":"719e2958-f412-44ca-b94a-b00060a7fccb","text":"Portrait Flick — approved contract fixture","visibility":"public","content_type":"flick","is_pinned":false,"created_at":"2026-08-16T19:44:32.520451Z","updated_at":"2026-08-16T19:44:32.609462Z","media":[{"media_id":"b52c17e1-d714-4250-93bd-0225b6898104","kind":"video"}],"counts":{"likes":0,"comments":0},"view_count":0,"is_bookmarked":false,"post_type":"video","app_origin":"posttube","share_to_postbook":true,"score":0.7253549379638999,"feed_content_type":"flick"}]}"""
    }
}
