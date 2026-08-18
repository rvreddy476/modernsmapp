package com.us.android.feature.post.ui

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.data.MediaApi
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.ProfileApi
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.core.profile.data.dto.GraphUserIdRequest
import com.us.android.core.profile.data.dto.PublicProfileDto
import com.us.android.core.profile.data.dto.UpdateProfileRequest
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.post.data.PostApi
import com.us.android.feature.post.data.PostRepository
import com.us.android.feature.post.data.dto.BookmarkStatusDto
import com.us.android.feature.post.data.dto.PostCountsDto
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

class PostViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    private class FakeApi : PostApi {
        var post: ApiEnvelope<PostDto> = ApiEnvelope(
            PostDto(
                id = "p",
                authorId = "a",
                // Explicit: the DTO defaults this to false, and a fixture that
                // relies on the default silently disables the repost control.
                isRepostable = true,
                counts = PostCountsDto(likes = 10, comments = 2),
            ),
        )

        /** Set to simulate a real HTTP failure rather than an envelope error. */
        var postThrows: Throwable? = null
        var reaction: ApiEnvelope<ReactionStatusDto> = ApiEnvelope(ReactionStatusDto("reacted"))
        var bookmark: ApiEnvelope<BookmarkStatusDto> = ApiEnvelope(BookmarkStatusDto(true))
        var repostResult: ApiEnvelope<RepostDto> = ApiEnvelope(RepostDto(id = "r"))
        var removeRepostThrows: Throwable? = null
        val calls = mutableListOf<String>()

        override suspend fun getPost(postId: String): ApiEnvelope<PostDto> {
            calls += "get"
            postThrows?.let { throw it }
            return post
        }
        override suspend fun addReaction(postId: String, body: ReactionRequest) =
            reaction.also { calls += "addReaction(${body.reaction})" }

        override suspend fun removeReaction(postId: String) =
            reaction.also { calls += "removeReaction" }

        override suspend fun setBookmark(postId: String) =
            bookmark.also { calls += "setBookmark" }

        override suspend fun clearBookmark(postId: String) =
            bookmark.also { calls += "clearBookmark" }

        override suspend fun repost(postId: String, body: RepostRequest) =
            repostResult.also { calls += "repost(${body.type})" }

        override suspend fun removeRepost(postId: String) {
            calls += "removeRepost"
            removeRepostThrows?.let { throw it }
        }

        // Throws rather than returning an empty page: the post screen must
        // never fetch comments. If it starts doing so, this fails loudly
        // instead of the call passing unnoticed.
        override suspend fun getComments(postId: String, limit: Int): Nothing =
            error("the post screen must not load comments")
    }

    /**
     * Answers the author lookup and refuses everything else.
     *
     * The post screen resolves ONE public profile. If it ever starts
     * following, blocking or writing a profile, these fail loudly rather than
     * letting the call pass unnoticed.
     */
    private class FakeProfileApi(private val displayName: String?) : ProfileApi {
        override suspend fun getProfile(userId: String) = ApiEnvelope(
            data = displayName?.let { PublicProfileDto(userId = userId, displayName = it) },
            error = null,
        )

        override suspend fun getOwnProfile(): Nothing = error("post screen must not read /me")
        override suspend fun updateProfile(body: UpdateProfileRequest): Nothing =
            error("post screen must not write a profile")
        override suspend fun getStats(userId: String): Nothing =
            error("post screen must not read stats")
        override suspend fun follow(body: GraphUserIdRequest): Nothing =
            error("post screen must not follow")
        override suspend fun unfollow(body: GraphUserIdRequest): Nothing =
            error("post screen must not unfollow")
        override suspend fun block(body: GraphUserIdRequest): Nothing =
            error("post screen must not block")
        override suspend fun unblock(body: GraphUserIdRequest): Nothing =
            error("post screen must not unblock")
    }

    /**
     * The post screen resolves at most ONE asset — the first. Anything else
     * fails loudly here rather than quietly costing the reader their data.
     */
    private class FakeMediaApi : MediaApi {
        override suspend fun getDelivery(mediaId: String): Nothing =
            error("this post carries no media; nothing should be resolved")
    }

    private fun viewModel(api: FakeApi, authorName: String? = null) = PostViewModel(
        repository = PostRepository(api, ErrorMapper(json)),
        profiles = ProfileRepository(FakeProfileApi(authorName), ErrorMapper(json)),
        media = MediaRepository(
            FakeMediaApi(),
            MediaUrlResolver(
                ApiConfig(
                    baseUrl = "http://127.0.0.1:8080",
                    wsBaseUrl = "ws://127.0.0.1:8093",
                    clientVersion = "test",
                    environment = "test",
                    isDebug = true,
                ),
            ),
            ErrorMapper(json),
        ),
        savedStateHandle = SavedStateHandle(mapOf("postId" to "p")),
    )

    private fun content(vm: PostViewModel) = vm.state.value as PostUiState.Content

    @Test
    fun `loads the post`() = runTest {
        val api = FakeApi()

        val state = viewModel(api).state.value

        assertThat(state).isInstanceOf(PostUiState.Content::class.java)
        assertThat((state as PostUiState.Content).post.counts.likes).isEqualTo(10)
    }

    /**
     * The post payload carries only `author_id`. The name comes from a second
     * call to the public profile endpoint, which is why this is asserted
     * separately from the post load.
     */
    @Test
    fun `the author header is resolved from the profile endpoint`() = runTest {
        val state = viewModel(FakeApi(), authorName = "RaghuVaran").state.value

        assertThat((state as PostUiState.Content).author?.nameForDisplay).isEqualTo("RaghuVaran")
    }

    /**
     * A missing profile must not cost the reader the post. The content is
     * already in hand; only the header is unknown.
     */
    @Test
    fun `a failed author lookup leaves the post readable`() = runTest {
        val state = viewModel(FakeApi(), authorName = null).state.value

        assertThat(state).isInstanceOf(PostUiState.Content::class.java)
        assertThat((state as PostUiState.Content).author).isNull()
    }

    @Test
    fun `a load failure is retryable`() = runTest {
        val api = FakeApi().apply { post = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR")) }

        val state = viewModel(api).state.value as PostUiState.Error

        assertThat(state.retryable).isTrue()
    }

    /**
     * A deleted post must not offer a retry that re-fetches the same 404.
     *
     * Thrown as a real [HttpException] rather than an error envelope, because
     * that is how a 404 actually arrives: only HTTP-status failures reach
     * ErrorMapper and become a typed AppError.NotFound. An error carried
     * inside a 200 envelope maps to Unknown, so faking it that way would test
     * a path the server never produces.
     */
    @Test
    fun `a not-found post is not retryable`() = runTest {
        val api = FakeApi().apply {
            postThrows = HttpException(
                Response.error<Unit>(
                    HTTP_NOT_FOUND,
                    """{"error":{"code":"NOT_FOUND","message":"Post not found"}}"""
                        .toResponseBody("application/json".toMediaType()),
                ),
            )
        }

        val state = viewModel(api).state.value as PostUiState.Error

        assertThat(state.retryable).isFalse()
        assertThat(state.message).contains("deleted")
    }

    @Test
    fun `liking moves the flag and the count together`() = runTest {
        val vm = viewModel(FakeApi())

        vm.onReactToggle()

        val post = content(vm).post
        assertThat(post.viewer.hasReacted).isTrue()
        assertThat(post.counts.likes).isEqualTo(11)
    }

    @Test
    fun `a failed like rolls back both the flag and the count`() = runTest {
        val api = FakeApi().apply { reaction = ApiEnvelope(error = ApiErrorBody(code = "SERVER")) }
        val vm = viewModel(api)

        vm.onReactToggle()

        val state = content(vm)
        assertThat(state.post.viewer.hasReacted).isFalse()
        assertThat(state.post.counts.likes).isEqualTo(10)
        assertThat(state.actionError).isNotNull()
    }

    @Test
    fun `unliking calls the delete endpoint`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onReactToggle()

        vm.onReactToggle()

        assertThat(api.calls).contains("removeReaction")
        assertThat(content(vm).post.counts.likes).isEqualTo(10)
    }

    /** The author turned reactions off; the control must do nothing. */
    @Test
    fun `liking is ignored when the author disabled reactions`() = runTest {
        val api = FakeApi().apply {
            post = ApiEnvelope(PostDto(id = "p", noLikes = true))
        }
        val vm = viewModel(api)

        vm.onReactToggle()

        assertThat(api.calls).doesNotContain("addReaction(like)")
    }

    /**
     * Saving now uses SET, not a toggle, so the direction is stated by the
     * client. Sending POST when the user meant to unsave would silently
     * re-save, so the endpoint choice is asserted, not just the outcome.
     */
    @Test
    fun `saving calls set and unsaving calls clear`() = runTest {
        val api = FakeApi().apply { bookmark = ApiEnvelope(BookmarkStatusDto(bookmarked = true)) }
        val vm = viewModel(api)

        vm.onBookmarkToggle()

        assertThat(api.calls).contains("setBookmark")
        assertThat(content(vm).post.viewer.isBookmarked).isTrue()

        api.bookmark = ApiEnvelope(BookmarkStatusDto(bookmarked = false))
        vm.onBookmarkToggle()

        assertThat(api.calls).contains("clearBookmark")
        assertThat(content(vm).post.viewer.isBookmarked).isFalse()
    }

    /** Still adopts the server's value, so a future divergence surfaces. */
    @Test
    fun `bookmark adopts the server's reported state`() = runTest {
        val api = FakeApi().apply { bookmark = ApiEnvelope(BookmarkStatusDto(bookmarked = false)) }
        val vm = viewModel(api)

        vm.onBookmarkToggle()

        assertThat(content(vm).post.viewer.isBookmarked).isFalse()
    }

    @Test
    fun `a failed bookmark leaves the state untouched and reports`() = runTest {
        val api = FakeApi().apply { bookmark = ApiEnvelope(error = ApiErrorBody(code = "SERVER")) }
        val vm = viewModel(api)

        vm.onBookmarkToggle()

        val state = content(vm)
        assertThat(state.post.viewer.isBookmarked).isFalse()
        assertThat(state.actionError).isNotNull()
        assertThat(state.busy).isFalse()
    }

    @Test
    fun `reposting increments the count and flips the session flag`() = runTest {
        val vm = viewModel(FakeApi())

        vm.onRepostToggle()

        val state = content(vm)
        assertThat(state.hasReposted).isTrue()
        assertThat(state.post.counts.reposts).isEqualTo(1)
    }

    @Test
    fun `undoing a repost calls delete and decrements`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onRepostToggle()

        vm.onRepostToggle()

        assertThat(api.calls).contains("removeRepost")
        assertThat(content(vm).hasReposted).isFalse()
        assertThat(content(vm).post.counts.reposts).isEqualTo(0)
    }

    /** Counts are server truth; a local decrement must never go negative. */
    @Test
    fun `counts never go below zero`() = runTest {
        val api = FakeApi().apply {
            post = ApiEnvelope(PostDto(id = "p", counts = PostCountsDto(likes = 0)))
        }
        val vm = viewModel(api)

        // Like then unlike from a zero baseline.
        vm.onReactToggle()
        vm.onReactToggle()

        assertThat(content(vm).post.counts.likes).isAtLeast(0)
    }

    @Test
    fun `dismissing the action error keeps the post`() = runTest {
        val api = FakeApi().apply { bookmark = ApiEnvelope(error = ApiErrorBody(code = "SERVER")) }
        val vm = viewModel(api)
        vm.onBookmarkToggle()

        vm.dismissActionError()

        assertThat(content(vm).actionError).isNull()
        assertThat(content(vm).post.id).isEqualTo("p")
    }
}
