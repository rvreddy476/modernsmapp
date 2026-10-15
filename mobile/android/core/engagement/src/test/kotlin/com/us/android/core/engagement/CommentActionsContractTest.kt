// Fixtures are single-line JSON bodies; wrapping them would change what they say.
@file:Suppress("MaxLineLength", "MaximumLineLength")

package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.CommentActionsApi
import com.us.android.core.engagement.data.CommentActionsRepository
import com.us.android.core.engagement.data.EngagementApi
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.ReportApi
import com.us.android.core.engagement.data.ReportOutcome
import com.us.android.core.engagement.data.ReportRepository
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
 * The comment-level routes on the wire (2026-10-14, audit T-17): method,
 * path, query, body and headers, against post-service's own route table and
 * request structs —
 *
 *  - `internal/http/handler.go` registers the `/v1/comments` group:
 *    `POST /:commentId/reply` (behind `idempotent`), `GET /:commentId/replies`,
 *    `PUT|DELETE /:commentId/reaction`, `DELETE /:commentId`,
 *    `POST|DELETE /:commentId/heart`, `PUT|DELETE /:commentId/pin`;
 *  - `comment_routes.go`: `commentReactionRequest{emoji}` and the summary
 *    `{emoji, reaction_count, reactions, viewer_reaction}`;
 *  - `CommentRequest{text}` is the reply body, as for a top-level comment;
 *  - `comment_creator.go`: `{comment_id, hearted_by_author}` and
 *    `{comment_id, post_id, pinned}`;
 *  - trust-safety's report intake: `entity_type` in
 *    `oneof=user post comment reel video`.
 *
 * The response bodies below are assembled from those Go struct tags, NOT
 * captured from a running server — no live capture of these routes exists
 * yet. Recapture and replace them when one does.
 */
class CommentActionsContractTest {

    private lateinit var server: MockWebServer
    private lateinit var actions: CommentActionsRepository
    private lateinit var engagement: EngagementRepository
    private lateinit var reports: ReportRepository
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        actions = CommentActionsRepository(retrofit.create(CommentActionsApi::class.java), ErrorMapper(json))
        engagement = EngagementRepository(retrofit.create(EngagementApi::class.java), ErrorMapper(json))
        reports = ReportRepository(retrofit.create(ReportApi::class.java), ErrorMapper(json))
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

    @Test
    fun `replies page - GET with limit and the cursor, oldest first, parent kept`() = runTest {
        enqueue(200, REPLIES_PAGE)

        val page = (actions.replies("c1", cursor = "cur-1") as AppResult.Success).data

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/comments/c1/replies?limit=20&cursor=cur-1")
        assertThat(page.items.map { it.id }).containsExactly("r1", "r2").inOrder()
        assertThat(page.items.first().parentId).isEqualTo("c1")
        assertThat(page.nextCursor).isEqualTo("next-2")
    }

    @Test
    fun `the first replies page sends no cursor`() = runTest {
        enqueue(200, """{"data":[]}""")

        actions.replies("c1")

        assertThat(server.takeRequest().target).isEqualTo("/v1/comments/c1/replies?limit=20")
    }

    @Test
    fun `reply - POST text with the idempotency key`() = runTest {
        enqueue(201, REPLY_CREATED)

        val reply = (actions.reply("c1", "same here", "key-1") as AppResult.Success).data

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/comments/c1/reply")
        assertThat(request.headers["Idempotency-Key"]).isEqualTo("key-1")
        assertThat(request.body?.utf8()).isEqualTo("""{"text":"same here"}""")
        assertThat(reply.isReply).isTrue()
        assertThat(reply.parentId).isEqualTo("c1")
    }

    @Test
    fun `like - PUT reaction with the heart emoji, answered with the summary`() = runTest {
        enqueue(200, """{"data":{"emoji":"❤️","reaction_count":5,"reactions":[{"emoji":"❤️","count":5}],"viewer_reaction":"❤️"}}""")

        val summary = (actions.setLiked("c1", liked = true) as AppResult.Success).data

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("PUT")
        assertThat(request.target).isEqualTo("/v1/comments/c1/reaction")
        assertThat(request.body?.utf8()).isEqualTo("""{"emoji":"❤️"}""")
        assertThat(summary.reactionCount).isEqualTo(5)
        assertThat(summary.viewerReaction).isEqualTo("❤️")
    }

    @Test
    fun `unlike - DELETE reaction, answered 200 with emoji and viewer_reaction null`() = runTest {
        enqueue(200, """{"data":{"emoji":null,"reaction_count":4,"reactions":[],"viewer_reaction":null}}""")

        val summary = (actions.setLiked("c1", liked = false) as AppResult.Success).data

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.target).isEqualTo("/v1/comments/c1/reaction")
        assertThat(summary.viewerReaction).isNull()
        assertThat(summary.reactionCount).isEqualTo(4)
    }

    @Test
    fun `delete - DELETE the comment`() = runTest {
        enqueue(200, """{"data":{"status":"deleted"}}""")

        assertThat(actions.delete("c1")).isInstanceOf(AppResult.Success::class.java)

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.target).isEqualTo("/v1/comments/c1")
    }

    @Test
    fun `heart and unheart - POST and DELETE heart`() = runTest {
        enqueue(200, """{"data":{"comment_id":"c1","hearted_by_author":true}}""")
        enqueue(200, """{"data":{"comment_id":"c1","hearted_by_author":false}}""")

        actions.setHearted("c1", hearted = true)
        actions.setHearted("c1", hearted = false)

        val on = server.takeRequest()
        val off = server.takeRequest()
        assertThat(on.method to on.target).isEqualTo("POST" to "/v1/comments/c1/heart")
        assertThat(off.method to off.target).isEqualTo("DELETE" to "/v1/comments/c1/heart")
    }

    @Test
    fun `pin and unpin - PUT and DELETE pin`() = runTest {
        enqueue(200, """{"data":{"comment_id":"c1","post_id":"p1","pinned":true}}""")
        enqueue(200, """{"data":{"comment_id":"c1","post_id":"p1","pinned":false}}""")

        actions.setPinned("c1", pinned = true)
        actions.setPinned("c1", pinned = false)

        val on = server.takeRequest()
        val off = server.takeRequest()
        assertThat(on.method to on.target).isEqualTo("PUT" to "/v1/comments/c1/pin")
        assertThat(off.method to off.target).isEqualTo("DELETE" to "/v1/comments/c1/pin")
    }

    @Test
    fun `a pin on a reply is refused 422 CANNOT_PIN_REPLY and surfaces as a failure`() = runTest {
        enqueue(422, """{"error":{"code":"CANNOT_PIN_REPLY","message":"only a top-level comment can be pinned"}}""")

        assertThat(actions.setPinned("r1", pinned = true)).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `report - entity_type comment with the comment id`() = runTest {
        enqueue(200, """{"data":{"id":"rep-1","status":"open"}}""")

        val outcome = reports.reportComment("c1", "harassment", "")

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/reports")
        assertThat(request.body?.utf8())
            .isEqualTo("""{"entity_type":"comment","entity_id":"c1","reason":"harassment"}""")
        assertThat(outcome).isEqualTo(ReportOutcome.Filed)
    }

    @Test
    fun `the list carries the viewer's reaction, the counts and the creator's marks`() = runTest {
        enqueue(200, LIST_WITH_CREATOR_MARKS)

        val comment = (engagement.comments("p1") as AppResult.Success).data.items.single()

        assertThat(comment.reactionCount).isEqualTo(3)
        assertThat(comment.viewerReaction).isEqualTo("🔥")
        assertThat(comment.pinned).isTrue()
        assertThat(comment.heartedByAuthor).isTrue()
        assertThat(comment.reply?.parentId).isEqualTo("c1")
        assertThat(comment.author?.username).isEqualTo("bob")
    }

    private companion object {
        const val REPLIES_PAGE =
            """{"data":[{"id":"r1","post_id":"p1","author_id":"u1","parent_id":"c1","body":"first","like_count":0,"dislike_count":0,"reply_count":0,"is_reply":true,"created_at":"2026-10-14T10:00:00Z","updated_at":"2026-10-14T10:00:00Z","reactions":[],"reaction_count":0,"viewer_reaction":null,"pinned":false,"hearted_by_author":false},{"id":"r2","post_id":"p1","author_id":"u2","parent_id":"c1","body":"second","like_count":0,"dislike_count":0,"reply_count":0,"is_reply":true,"created_at":"2026-10-14T10:01:00Z","updated_at":"2026-10-14T10:01:00Z","reactions":[],"reaction_count":0,"viewer_reaction":null,"pinned":false,"hearted_by_author":false}],"meta":{"next_cursor":"next-2"}}"""

        const val REPLY_CREATED =
            """{"data":{"id":"r9","post_id":"p1","author_id":"me","parent_id":"c1","body":"same here","like_count":0,"dislike_count":0,"reply_count":0,"is_reply":true,"created_at":"2026-10-14T10:02:00Z","updated_at":"2026-10-14T10:02:00Z","reactions":[],"reaction_count":0,"viewer_reaction":null,"pinned":false,"hearted_by_author":false}}"""

        const val LIST_WITH_CREATOR_MARKS =
            """{"data":[{"id":"c1","post_id":"p1","author_id":"u2","body":"nice","like_count":3,"dislike_count":0,"reply_count":1,"is_reply":false,"created_at":"2026-10-14T09:00:00Z","updated_at":"2026-10-14T09:00:00Z","reactions":[{"emoji":"🔥","count":2},{"emoji":"❤️","count":1}],"reaction_count":3,"viewer_reaction":"🔥","pinned":true,"hearted_by_author":true,"reply":{"id":"r1","post_id":"p1","author_id":"u3","parent_id":"c1","body":"agreed","like_count":0,"dislike_count":0,"reply_count":0,"is_reply":true,"created_at":"2026-10-14T09:05:00Z","updated_at":"2026-10-14T09:05:00Z","reactions":[],"reaction_count":0,"viewer_reaction":null,"pinned":false,"hearted_by_author":false},"author":{"id":"u2","username":"bob","display_name":"Bob"}}]}"""
    }
}
