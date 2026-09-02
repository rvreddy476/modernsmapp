package com.us.android.feature.post.ui

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.engagement.data.EngagementRepository
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
import com.us.android.core.profile.data.dto.UpdateMediaIdRequest
import com.us.android.core.profile.data.dto.UpdateProfileRequest
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.post.data.PostApi
import com.us.android.feature.post.data.PostRepository
import com.us.android.feature.post.data.dto.PostCountsDto
import com.us.android.feature.post.data.dto.PostDto
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
        val calls = mutableListOf<String>()

        override suspend fun getPost(postId: String): ApiEnvelope<PostDto> {
            calls += "get"
            postThrows?.let { throw it }
            return post
        }

        // Creation is not under test here; the composer has its own suite.
        override suspend fun createPost(
            idempotencyKey: String,
            body: com.us.android.feature.post.data.dto.CreatePostRequest,
        ): Nothing = error("not used")
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
        override suspend fun updateAvatar(body: UpdateMediaIdRequest): Nothing =
            error("post screen must not write an avatar")
        override suspend fun updateCover(body: UpdateMediaIdRequest): Nothing =
            error("post screen must not write a cover")
        override suspend fun getStats(userId: String): Nothing =
            error("post screen must not read stats")
        override suspend fun relationship(userId: String, otherId: String): Nothing =
            error("post screen must not read relationships")
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

    private val engagementRepository by lazy {
        EngagementRepository(NoEngagementApi(), ErrorMapper(json))
    }

    /**
     * Refuses every engagement call.
     *
     * This screen must not perform engagement writes during a plain load —
     * if one starts firing, this fails loudly rather than passing unnoticed.
     */
    private class NoEngagementApi : com.us.android.core.engagement.data.EngagementApi {
        override suspend fun addReaction(
            postId: String,
            body: com.us.android.core.engagement.data.ReactionRequest,
        ): Nothing = error("no engagement expected")
        override suspend fun removeReaction(postId: String): Nothing = error("no engagement expected")
        override suspend fun addBookmark(postId: String): Nothing = error("no engagement expected")
        override suspend fun removeBookmark(postId: String): Nothing = error("no engagement expected")
        override suspend fun repost(
            postId: String,
            body: com.us.android.core.engagement.data.RepostRequest,
        ): Nothing = error("no engagement expected")
        override suspend fun removeRepost(postId: String): Nothing = error("no engagement expected")
        override suspend fun share(
            postId: String,
            body: com.us.android.core.engagement.data.ShareRequest,
        ): Nothing = error("no engagement expected")
        override suspend fun getComments(
            postId: String,
            limit: Int,
            cursor: String?,
        ): Nothing = error("the post screen must not load comments")
        override suspend fun addComment(
            postId: String,
            idempotencyKey: String,
            body: com.us.android.core.engagement.data.CreateCommentRequest,
        ): Nothing = error("no engagement expected")
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
        engagement = com.us.android.core.engagement.data.EngagementStore(engagementRepository),
        engagementRepository = engagementRepository,
        savedStateHandle = SavedStateHandle(mapOf("postId" to "p")),
    )

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

    // Reaction, bookmark and repost behaviour is no longer asserted here.
    // Those mutations moved to the shared EngagementStore, and their real
    // risks — response ordering, partial rollback and count derivation — are
    // covered by :core:engagement's EngagementStoreTest, with the wire
    // payloads pinned by EngagementContractTest. Re-testing them through this
    // ViewModel would assert delegation rather than behaviour.
}
