package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.engagement.data.CommentDto
import com.us.android.core.engagement.data.CommentsController
import com.us.android.core.engagement.data.EngagementApi
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test
import java.io.IOException

/**
 * Comment paging, composer validation, idempotency and retry.
 *
 * Driven through the real [EngagementRepository] against a fake [EngagementApi]
 * so the envelope and `meta.next_cursor` handling are exercised rather than
 * stubbed over.
 */
class CommentsControllerTest {

    private val json = Json { ignoreUnknownKeys = true }
    private val postId = "p1"

    private class FakeApi : EngagementApi {
        /** Queued list responses, consumed in order. */
        val pages = ArrayDeque<() -> ApiEnvelope<List<CommentDto>>>()
        val requestedCursors = mutableListOf<String?>()

        /** Queued create responses, consumed in order. */
        val creates = ArrayDeque<() -> ApiEnvelope<CommentDto>>()
        val idempotencyKeys = mutableListOf<String>()

        /** What was actually sent, so a replayed key cannot hide changed text. */
        val sentTexts = mutableListOf<String>()

        override suspend fun getComments(
            postId: String,
            limit: Int,
            cursor: String?,
        ): ApiEnvelope<List<CommentDto>> {
            requestedCursors += cursor
            return pages.removeFirst().invoke()
        }

        override suspend fun addComment(
            postId: String,
            idempotencyKey: String,
            body: com.us.android.core.engagement.data.CreateCommentRequest,
        ): ApiEnvelope<CommentDto> {
            idempotencyKeys += idempotencyKey
            sentTexts += body.text
            return creates.removeFirst().invoke()
        }

        override suspend fun addReaction(postId: String, body: com.us.android.core.engagement.data.ReactionRequest) =
            error("not used")

        override suspend fun removeReaction(postId: String) = error("not used")
        override suspend fun addBookmark(postId: String) = error("not used")
        override suspend fun removeBookmark(postId: String) = error("not used")
        override suspend fun repost(postId: String, body: com.us.android.core.engagement.data.RepostRequest) =
            error("not used")

        override suspend fun removeRepost(postId: String) = error("not used")
        override suspend fun share(postId: String, body: com.us.android.core.engagement.data.ShareRequest) =
            error("not used")
    }

    private fun controller(api: FakeApi) =
        CommentsController(postId, EngagementRepository(api, ErrorMapper(json)))

    private fun comment(id: String) = CommentDto(id = id, postId = postId, authorId = "a", body = "c$id")

    private fun page(vararg ids: String, next: String? = null) = {
        ApiEnvelope(data = ids.map(::comment), meta = ApiMeta(nextCursor = next))
    }

    private fun boom(): () -> Nothing = { throw IOException("offline") }

    @Test
    fun `the first page is requested without a cursor and consumes next_cursor`() = runTest {
        val api = FakeApi().apply { pages += page("1", "2", next = "cur-1") }

        val state = controller(api).refresh()

        assertThat(api.requestedCursors).containsExactly(null)
        assertThat(state.rows.map { it.id }).containsExactly("1", "2").inOrder()
        assertThat(state.nextCursor).isEqualTo("cur-1")
        assertThat(state.canLoadMore).isTrue()
    }

    @Test
    fun `append sends the cursor and adds the next page`() = runTest {
        val api = FakeApi().apply {
            pages += page("1", next = "cur-1")
            pages += page("2", next = null)
        }
        val controller = controller(api)
        controller.refresh()

        val state = controller.loadMore()

        assertThat(api.requestedCursors).containsExactly(null, "cur-1").inOrder()
        assertThat(state.rows.map { it.id }).containsExactly("1", "2").inOrder()
        assertThat(state.nextCursor).isNull()
        assertThat(state.canLoadMore).isFalse()
    }

    /**
     * A comment inserted at the page boundary can be returned twice. Appending
     * it blindly duplicates a row and crashes a keyed list.
     */
    @Test
    fun `a row repeated across pages is not duplicated`() = runTest {
        val api = FakeApi().apply {
            pages += page("1", "2", next = "cur-1")
            pages += page("2", "3", next = null)
        }
        val controller = controller(api)
        controller.refresh()

        val state = controller.loadMore()

        assertThat(state.rows.map { it.id }).containsExactly("1", "2", "3").inOrder()
    }

    @Test
    fun `an empty page ends pagination without error`() = runTest {
        val api = FakeApi().apply {
            pages += page("1", next = "cur-1")
            pages += page(next = null)
        }
        val controller = controller(api)
        controller.refresh()

        val state = controller.loadMore()

        assertThat(state.rows.map { it.id }).containsExactly("1")
        assertThat(state.appendError).isNull()
        assertThat(state.canLoadMore).isFalse()
    }

    /**
     * A failed append must keep what is already loaded and retry the SAME
     * cursor, so no page is skipped and none is discarded.
     */
    @Test
    fun `a failed append keeps loaded rows and retries the same cursor`() = runTest {
        val api = FakeApi().apply {
            pages += page("1", next = "cur-1")
            pages += boom()
            pages += page("2", next = null)
        }
        val controller = controller(api)
        controller.refresh()

        val failed = controller.loadMore()
        assertThat(failed.rows.map { it.id }).containsExactly("1")
        assertThat(failed.appendError).isNotNull()

        val retried = controller.loadMore()
        assertThat(api.requestedCursors).containsExactly(null, "cur-1", "cur-1").inOrder()
        assertThat(retried.rows.map { it.id }).containsExactly("1", "2").inOrder()
    }

    /** A refresh failure over loaded rows must not blank the list. */
    @Test
    fun `a failed refresh preserves already loaded rows`() = runTest {
        val api = FakeApi().apply {
            pages += page("1", next = null)
            pages += boom()
        }
        val controller = controller(api)
        controller.refresh()

        val state = controller.refresh()

        assertThat(state.rows.map { it.id }).containsExactly("1")
        assertThat(state.refreshError).isNotNull()
    }

    // ── Composer ────────────────────────────────────────────────────────

    @Test
    fun `blank and oversized drafts cannot be submitted`() = runTest {
        val controller = controller(FakeApi().apply { pages += page(next = null) })
        controller.refresh()

        assertThat(controller.onDraftChange("   ").canSubmit).isFalse()
        assertThat(controller.onDraftChange("").canSubmit).isFalse()
        assertThat(controller.onDraftChange("x".repeat(2_001)).canSubmit).isFalse()
        assertThat(controller.onDraftChange("real comment").canSubmit).isTrue()
    }

    @Test
    fun `a successful submit sends text once and clears the draft`() = runTest {
        val api = FakeApi().apply {
            pages += page(next = null)
            creates += { ApiEnvelope(data = comment("new")) }
        }
        val controller = controller(api)
        controller.refresh()
        controller.onDraftChange("hello")

        val state = controller.submit()

        assertThat(api.idempotencyKeys).hasSize(1)
        assertThat(state.rows.map { it.id }).containsExactly("new")
        assertThat(state.rows.single().pending).isFalse()
        assertThat(state.draft).isEmpty()
        assertThat(state.submitError).isNull()
    }

    /**
     * The heart of comment idempotency: a retry after a failure must reuse the
     * key, so a request that actually reached the server is replayed rather
     * than posted a second time — and exactly one comment exists afterwards.
     */
    @Test
    fun `a retried submit reuses the idempotency key and inserts one comment`() = runTest {
        val api = FakeApi().apply {
            pages += page(next = null)
            creates += boom()
            creates += { ApiEnvelope(data = comment("new")) }
        }
        val controller = controller(api)
        controller.refresh()
        controller.onDraftChange("hello")

        val failed = controller.submit()
        assertThat(failed.submitError).isNotNull()
        // The draft survives so the user does not retype it.
        assertThat(failed.draft).isEqualTo("hello")
        // The optimistic row is withdrawn; nothing pretends to be posted.
        assertThat(failed.rows).isEmpty()

        val retried = controller.submit()

        assertThat(api.idempotencyKeys).hasSize(2)
        assertThat(api.idempotencyKeys[0]).isEqualTo(api.idempotencyKeys[1])
        assertThat(retried.rows.map { it.id }).containsExactly("new")
    }

    /**
     * Editing the draft after a failure must mint a NEW key.
     *
     * The key is a promise that a repeat is the same intent. If the original
     * request actually succeeded and only its response was lost, reusing the
     * key for edited text makes the server replay the FIRST comment: the
     * viewer's correction is silently discarded and the text they rejected is
     * published as though they had written it.
     */
    @Test
    fun `editing after a failure mints a new key so the old text cannot replay`() = runTest {
        val api = FakeApi().apply {
            pages += page(next = null)
            creates += boom()
            creates += { ApiEnvelope(data = comment("edited")) }
        }
        val controller = controller(api)
        controller.refresh()
        controller.onDraftChange("original text")

        val failed = controller.submit()
        assertThat(failed.submitError).isNotNull()

        controller.onDraftChange("corrected text")
        controller.submit()

        assertThat(api.idempotencyKeys).hasSize(2)
        assertThat(api.idempotencyKeys[0]).isNotEqualTo(api.idempotencyKeys[1])
        assertThat(api.sentTexts).containsExactly("original text", "corrected text").inOrder()
    }

    /**
     * Editing back to the original text is still the SAME intent, so the key
     * must be reused — otherwise a user who fixes a typo and undoes the fix
     * posts twice.
     */
    @Test
    fun `editing back to the original text reuses the key`() = runTest {
        val api = FakeApi().apply {
            pages += page(next = null)
            creates += boom()
            creates += { ApiEnvelope(data = comment("same")) }
        }
        val controller = controller(api)
        controller.refresh()
        controller.onDraftChange("hello")
        controller.submit()

        controller.onDraftChange("hello world")
        controller.onDraftChange("hello")
        controller.submit()

        assertThat(api.idempotencyKeys).hasSize(2)
        assertThat(api.idempotencyKeys[0]).isEqualTo(api.idempotencyKeys[1])
    }

    /** A second, different comment must NOT reuse the previous key. */
    @Test
    fun `the next comment gets a fresh idempotency key`() = runTest {
        val api = FakeApi().apply {
            pages += page(next = null)
            creates += { ApiEnvelope(data = comment("first")) }
            creates += { ApiEnvelope(data = comment("second")) }
        }
        val controller = controller(api)
        controller.refresh()

        controller.onDraftChange("one")
        controller.submit()
        controller.onDraftChange("two")
        controller.submit()

        assertThat(api.idempotencyKeys).hasSize(2)
        assertThat(api.idempotencyKeys[0]).isNotEqualTo(api.idempotencyKeys[1])
    }

    @Test
    fun `an error envelope is surfaced as a submit error`() = runTest {
        val api = FakeApi().apply {
            pages += page(next = null)
            creates += {
                ApiEnvelope<CommentDto>(
                    error = com.us.android.core.network.ApiErrorBody(
                        code = "COMMENTS_DISABLED",
                        message = "Comments are disabled",
                    ),
                )
            }
        }
        val controller = controller(api)
        controller.refresh()
        controller.onDraftChange("hello")

        val state = controller.submit()

        assertThat(state.submitError).isInstanceOf(AppError::class.java)
        assertThat(state.draft).isEqualTo("hello")
        assertThat(state.rows).isEmpty()
    }
}
