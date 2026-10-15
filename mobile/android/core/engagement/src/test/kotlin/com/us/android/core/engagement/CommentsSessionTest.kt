package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.engagement.data.CommentActionsRepository
import com.us.android.core.engagement.data.CommentDto
import com.us.android.core.engagement.data.CommentMentionSource
import com.us.android.core.engagement.data.CommentReactionSummaryDto
import com.us.android.core.engagement.data.CommentsController
import com.us.android.core.engagement.data.CommentsSession
import com.us.android.core.engagement.data.CommentsViewer
import com.us.android.core.engagement.data.CreateCommentRequest
import com.us.android.core.engagement.data.EngagementApi
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.MentionCandidate
import com.us.android.core.engagement.data.ReactionRequest
import com.us.android.core.engagement.data.ReportRepository
import com.us.android.core.engagement.data.RepostRequest
import com.us.android.core.engagement.data.ShareRequest
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * The coroutine wrapper every comments host shares: what reaches the
 * screen, and WHEN. The optimistic like must be on screen while its request
 * is still in flight — the whole point of being optimistic.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class CommentsSessionTest {

    private val mapper = ErrorMapper(Json { ignoreUnknownKeys = true })

    private class OneComment : EngagementApi {
        override suspend fun getComments(postId: String, limit: Int, cursor: String?) =
            ApiEnvelope(data = listOf(CommentDto(id = "c1", postId = postId, authorId = "other", body = "hi")))

        override suspend fun addComment(postId: String, idempotencyKey: String, body: CreateCommentRequest) =
            error("not used")

        override suspend fun addReaction(postId: String, body: ReactionRequest) = error("not used")
        override suspend fun removeReaction(postId: String) = error("not used")
        override suspend fun addBookmark(postId: String) = error("not used")
        override suspend fun removeBookmark(postId: String) = error("not used")
        override suspend fun repost(postId: String, body: RepostRequest) = error("not used")
        override suspend fun removeRepost(postId: String) = error("not used")
        override suspend fun share(postId: String, body: ShareRequest) = error("not used")
    }

    private fun controller(actions: FakeCommentActionsApi, mentions: CommentMentionSource = CommentMentionSource.None) =
        CommentsController(
            "p1",
            EngagementRepository(OneComment(), mapper),
            CommentActionsRepository(actions, mapper),
            ReportRepository(FakeReportApi(), mapper),
            mentions,
        )

    @Test
    fun `the optimistic like reaches the screen before the server answers`() = runTest {
        val actions = FakeCommentActionsApi()
        val gate = CompletableDeferred<Unit>()
        actions.answers["PUT /v1/comments/c1/reaction ❤️"] = {
            gate.await()
            ApiEnvelope(data = CommentReactionSummaryDto(reactionCount = 7, viewerReaction = "❤️"))
        }
        val session = CommentsSession(this)
        session.attach(controller(actions))
        session.open("author", { CommentsViewer("me", "Me") })
        advanceUntilIdle()

        session.toggleLike("c1")
        runCurrent()
        assertThat(session.state.value.rows.single().viewerLiked).isTrue()
        assertThat(session.state.value.rows.single().likeCount).isEqualTo(1)

        gate.complete(Unit)
        advanceUntilIdle()
        assertThat(session.state.value.rows.single().likeCount).isEqualTo(7)
    }

    @Test
    fun `open records the post author and the viewer for the menu`() = runTest {
        val session = CommentsSession(this)
        session.attach(controller(FakeCommentActionsApi()))

        session.open("me", { CommentsViewer("me", "Me") })
        advanceUntilIdle()

        val state = session.state.value
        assertThat(state.postAuthorId).isEqualTo("me")
        assertThat(state.viewer?.id).isEqualTo("me")
        assertThat(state.menuFor(state.rows.single()).map { it.name }).containsExactly("HEART", "PIN", "REPORT")
    }

    @Test
    fun `mention lookups are debounced to the last handle typed`() = runTest {
        val asked = mutableListOf<String>()
        val session = CommentsSession(this)
        session.attach(
            controller(FakeCommentActionsApi(), CommentMentionSource { q ->
                asked += q
                listOf(MentionCandidate("u1", "raghu", "Raghu"))
            }),
        )

        session.onDraftChange("@r")
        advanceTimeBy(100)
        session.onDraftChange("@ra")
        advanceUntilIdle()

        assertThat(asked).containsExactly("ra")
        assertThat(session.state.value.mentions.map { it.username }).containsExactly("raghu")
    }

    @Test
    fun `a controller replaced meanwhile cannot overwrite the new conversation`() = runTest {
        val slow = FakeCommentActionsApi()
        val gate = CompletableDeferred<Unit>()
        slow.answers["PUT /v1/comments/c1/reaction ❤️"] = {
            gate.await()
            ApiEnvelope(data = CommentReactionSummaryDto(reactionCount = 99, viewerReaction = "❤️"))
        }
        val session = CommentsSession(this)
        val first = controller(slow)
        session.attach(first)
        session.refresh()
        advanceUntilIdle()
        session.toggleLike("c1")
        runCurrent()

        session.attach(controller(FakeCommentActionsApi()))
        gate.complete(Unit)
        advanceUntilIdle()

        assertThat(session.state.value.rows).isEmpty()
    }
}
