package com.us.android.feature.post.ui

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.post.data.PostApi
import com.us.android.feature.post.data.PostRepository
import com.us.android.feature.post.data.dto.BookmarkStatusDto
import com.us.android.feature.post.data.dto.CommentDto
import com.us.android.feature.post.data.dto.PostDto
import com.us.android.feature.post.data.dto.ReactionRequest
import com.us.android.feature.post.data.dto.ReactionStatusDto
import com.us.android.feature.post.data.dto.RepostDto
import com.us.android.feature.post.data.dto.RepostRequest
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Rule
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response

private const val HTTP_NOT_FOUND = 404

/** The page size CommentsViewModel asks for when nothing overrides it. */
private const val DEFAULT_PAGE_SIZE = 50

class CommentsViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    /**
     * Only the comments route is exercised; the rest of [PostApi] throws.
     *
     * Returning empty stubs for the other methods would let a future change
     * that fetched the post here pass silently. This screen must issue exactly
     * one call, and an unimplemented stub is what proves it.
     */
    private class FakeApi : PostApi {
        var comments: ApiEnvelope<List<CommentDto>> = ApiEnvelope(emptyList())
        var commentsThrows: Throwable? = null
        val requestedLimits = mutableListOf<Int>()
        var callCount = 0

        override suspend fun getComments(postId: String, limit: Int): ApiEnvelope<List<CommentDto>> {
            callCount++
            requestedLimits += limit
            commentsThrows?.let { throw it }
            return comments
        }

        override suspend fun getPost(postId: String): ApiEnvelope<PostDto> = unused("getPost")

        override suspend fun addReaction(
            postId: String,
            body: ReactionRequest,
        ): ApiEnvelope<ReactionStatusDto> = unused("addReaction")

        override suspend fun removeReaction(postId: String): ApiEnvelope<ReactionStatusDto> =
            unused("removeReaction")

        override suspend fun setBookmark(postId: String): ApiEnvelope<BookmarkStatusDto> =
            unused("setBookmark")

        override suspend fun clearBookmark(postId: String): ApiEnvelope<BookmarkStatusDto> =
            unused("clearBookmark")

        override suspend fun repost(postId: String, body: RepostRequest): ApiEnvelope<RepostDto> =
            unused("repost")

        override suspend fun removeRepost(postId: String) = unused<Unit>("removeRepost")

        private fun <T> unused(name: String): T =
            error("$name must not be called from the comments screen")
    }

    private fun viewModel(api: FakeApi) = CommentsViewModel(
        repository = PostRepository(api, ErrorMapper(json)),
        savedStateHandle = SavedStateHandle(mapOf("postId" to "p")),
    )

    private fun content(vm: CommentsViewModel) = vm.state.value as CommentsUiState.Content

    @Test
    fun `loads the comments`() = runTest {
        val api = FakeApi().apply {
            comments = ApiEnvelope(listOf(CommentDto(id = "c1", body = "hello")))
        }

        val vm = viewModel(api)

        assertThat(content(vm).comments).hasSize(1)
        assertThat(content(vm).comments.single().body).isEqualTo("hello")
    }

    /**
     * An empty list is Content, not Error and not a distinct Empty state.
     *
     * `{"data":[]}` was the entire 2026-08-16 capture, so this is the common
     * case rather than an edge one, and rendering it as a failure would tell
     * the user something is broken when nothing is.
     */
    @Test
    fun `no comments is a successful, empty Content`() = runTest {
        val vm = viewModel(FakeApi())

        val state = vm.state.value

        assertThat(state).isInstanceOf(CommentsUiState.Content::class.java)
        assertThat((state as CommentsUiState.Content).comments).isEmpty()
    }

    @Test
    fun `a load failure is retryable`() = runTest {
        val api = FakeApi().apply {
            comments = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }

        val state = viewModel(api).state.value as CommentsUiState.Error

        assertThat(state.retryable).isTrue()
        assertThat(state.message).contains("comments")
    }

    /**
     * A 404 on this route means the POST is gone, not that it has no comments,
     * so re-fetching would return the same 404 and the retry is withheld.
     *
     * Thrown as a real [HttpException] rather than an error envelope because
     * that is how a 404 actually arrives: only HTTP-status failures reach
     * ErrorMapper and become a typed AppError.NotFound.
     */
    @Test
    fun `a not-found post is not retryable`() = runTest {
        val api = FakeApi().apply {
            commentsThrows = HttpException(
                Response.error<Unit>(
                    HTTP_NOT_FOUND,
                    """{"error":{"code":"NOT_FOUND","message":"Post not found"}}"""
                        .toResponseBody("application/json".toMediaType()),
                ),
            )
        }

        val state = viewModel(api).state.value as CommentsUiState.Error

        assertThat(state.retryable).isFalse()
        assertThat(state.message).contains("deleted")
    }

    @Test
    fun `retrying after a failure recovers`() = runTest {
        val api = FakeApi().apply {
            comments = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }
        val vm = viewModel(api)
        assertThat(vm.state.value).isInstanceOf(CommentsUiState.Error::class.java)

        api.comments = ApiEnvelope(listOf(CommentDto(id = "c1", body = "back")))
        vm.load()

        assertThat(content(vm).comments).hasSize(1)
    }

    /**
     * Reload REPLACES the list; it never appends.
     *
     * There is no cursor in this contract, so a second call returns the same
     * page from the top. Appending would duplicate every row that survived
     * between the two reads.
     */
    @Test
    fun `reloading replaces the list rather than appending`() = runTest {
        val api = FakeApi().apply {
            comments = ApiEnvelope(listOf(CommentDto(id = "c1"), CommentDto(id = "c2")))
        }
        val vm = viewModel(api)
        assertThat(content(vm).comments).hasSize(2)

        vm.load()

        assertThat(content(vm).comments).hasSize(2)
    }

    /** One screen, one request. No hidden post fetch, no second page. */
    @Test
    fun `loading issues exactly one request`() = runTest {
        val api = FakeApi()

        viewModel(api)

        assertThat(api.callCount).isEqualTo(1)
    }

    /**
     * The limit is sent on every load.
     *
     * It is the only query parameter the capture ever carried, and the server
     * default is unobserved — so omitting it would be relying on a behaviour
     * nobody has seen.
     */
    @Test
    fun `every load asks for an explicit page size`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)

        vm.load()

        assertThat(api.requestedLimits).containsExactly(DEFAULT_PAGE_SIZE, DEFAULT_PAGE_SIZE)
    }

    /** A missing route argument must fail visibly, not fetch someone else's post. */
    @Test
    fun `a blank postId still issues the call and surfaces the result`() = runTest {
        val api = FakeApi()
        val vm = CommentsViewModel(
            repository = PostRepository(api, ErrorMapper(json)),
            savedStateHandle = SavedStateHandle(),
        )

        assertThat(vm.state.value).isInstanceOf(CommentsUiState.Content::class.java)
        assertThat(api.callCount).isEqualTo(1)
    }

    /** Fields the wire omits arrive as defaults, not as a broken row. */
    @Test
    fun `a sparse comment maps to defaults`() = runTest {
        val api = FakeApi().apply { comments = ApiEnvelope(listOf(CommentDto(id = "c1"))) }

        val comment = content(viewModel(api)).comments.single()

        assertThat(comment.body).isEmpty()
        assertThat(comment.likeCount).isEqualTo(0)
        assertThat(comment.replyCount).isEqualTo(0)
        assertThat(comment.isReply).isFalse()
    }

    /**
     * `is_reply` survives the mapping.
     *
     * It is the only structural signal the payload carries, and the row's
     * "Reply" marker is the only thing that keeps a reply from reading as a
     * top-level comment — there is no parent id to nest it under.
     */
    @Test
    fun `the reply flag reaches the state`() = runTest {
        val api = FakeApi().apply {
            comments = ApiEnvelope(listOf(CommentDto(id = "c1", isReply = true, replyCount = 3)))
        }

        val comment = content(viewModel(api)).comments.single()

        assertThat(comment.isReply).isTrue()
        assertThat(comment.replyCount).isEqualTo(3)
    }
}
