package com.us.android.feature.post.ui

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.EngagementWrites
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.data.MediaApi
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.ProfileApi
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.core.profile.data.dto.GraphUserIdRequest
import com.us.android.core.profile.data.dto.UpdateMediaIdRequest
import com.us.android.core.profile.data.dto.UpdateProfileRequest
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.post.data.PostApi
import com.us.android.feature.post.data.PostRepository
import com.us.android.feature.post.data.dto.PostDto
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

/**
 * Per-viewer engagement state on post detail, from wire bytes to first tap.
 *
 * WHY THIS FILE EXISTS
 *
 * `PostDto` omitted `has_reacted`, `viewer_reaction` and `has_reposted`, and
 * `PostRepository` hardcoded `hasReacted = false`. Every existing test used a
 * fixture built from DTO defaults, so all three fields were false everywhere
 * and nothing noticed. The result was two broken journeys:
 *
 *  - a post the viewer had already liked rendered unliked, so the first tap
 *    POSTed another reaction instead of removing the existing one;
 *  - an already-reposted post rendered un-reposted, so the first tap POSTed,
 *    took `409 ALREADY_REPOSTED`, rolled back, and left no way to undo.
 *
 * The payload below is a VERBATIM capture taken on 2026-08-21 from
 * `GET /v1/posts/{id}` on post-service :8084 with an engaged viewer. Every
 * viewer field is deliberately non-default, which is the only shape that can
 * catch a client reading its own defaults instead of the server's answer.
 */
@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
class PostViewerStateContractTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    private class FakeApi(private val envelope: ApiEnvelope<PostDto>) : PostApi {
        override suspend fun getPost(postId: String) = envelope

        // Creation is not under test here; the composer has its own suite.
        override suspend fun createPost(
            idempotencyKey: String,
            body: com.us.android.feature.post.data.dto.CreatePostRequest,
        ): Nothing = error("not used")
    }

    private class FakeProfileApi : ProfileApi {
        override suspend fun getProfile(userId: String): Nothing =
            error("author lookup is not under test")

        override suspend fun getOwnProfile(): Nothing = error("not used")
        override suspend fun updateProfile(body: UpdateProfileRequest): Nothing = error("not used")
        override suspend fun updateAvatar(body: UpdateMediaIdRequest): Nothing = error("not used")
        override suspend fun updateCover(body: UpdateMediaIdRequest): Nothing = error("not used")
        override suspend fun getStats(userId: String): Nothing = error("not used")
        override suspend fun relationship(userId: String, otherId: String): Nothing =
            error("not used")
        override suspend fun sendConnectionRequest(body: GraphUserIdRequest): Nothing =
            error("not used")
        override suspend fun acceptConnectionRequest(body: GraphUserIdRequest): Nothing =
            error("not used")
        override suspend fun follow(body: GraphUserIdRequest): Nothing = error("not used")
        override suspend fun unfollow(body: GraphUserIdRequest): Nothing = error("not used")
        override suspend fun block(body: GraphUserIdRequest): Nothing = error("not used")
        override suspend fun unblock(body: GraphUserIdRequest): Nothing = error("not used")
    }

    private class FakeMediaApi : MediaApi {
        override suspend fun getDelivery(mediaId: String): Nothing =
            error("media is not under test")
    }

    /** Records which HTTP verb the first tap actually chose. */
    private class RecordingWrites : EngagementWrites {
        val calls = mutableListOf<String>()

        override suspend fun react(postId: String, reaction: String): AppResult<Unit> {
            calls += "POST /reactions"
            return AppResult.Success(Unit)
        }

        override suspend fun unreact(postId: String): AppResult<Unit> {
            calls += "DELETE /reactions"
            return AppResult.Success(Unit)
        }

        override suspend fun setBookmarked(postId: String, bookmarked: Boolean): AppResult<Unit> {
            calls += if (bookmarked) "POST /bookmark" else "DELETE /bookmark"
            return AppResult.Success(Unit)
        }

        override suspend fun repost(postId: String): AppResult<Unit> {
            calls += "POST /repost"
            return AppResult.Success(Unit)
        }

        override suspend fun removeRepost(postId: String): AppResult<Unit> {
            calls += "DELETE /repost"
            return AppResult.Success(Unit)
        }
    }

    /**
     * Refuses every engagement call. The store under test is driven through
     * [RecordingWrites]; anything reaching the API means the ViewModel took a
     * path this test is not modelling.
     */
    private class UnusedEngagementApi : com.us.android.core.engagement.data.EngagementApi {
        override suspend fun addReaction(
            postId: String,
            body: com.us.android.core.engagement.data.ReactionRequest,
        ): Nothing = error("writes go through EngagementWrites in this test")

        override suspend fun removeReaction(postId: String): Nothing = error("unused")
        override suspend fun addBookmark(postId: String): Nothing = error("unused")
        override suspend fun removeBookmark(postId: String): Nothing = error("unused")
        override suspend fun repost(
            postId: String,
            body: com.us.android.core.engagement.data.RepostRequest,
        ): Nothing = error("unused")

        override suspend fun removeRepost(postId: String): Nothing = error("unused")
        override suspend fun share(
            postId: String,
            body: com.us.android.core.engagement.data.ShareRequest,
        ): Nothing = error("unused")

        override suspend fun getComments(
            postId: String,
            limit: Int,
            cursor: String?,
        ): Nothing = error("the post screen must not load comments")

        override suspend fun addComment(
            postId: String,
            idempotencyKey: String,
            body: com.us.android.core.engagement.data.CreateCommentRequest,
        ): Nothing = error("unused")
    }

    private fun decodeCaptured(): ApiEnvelope<PostDto> = json.decodeFromString(ENGAGED_CAPTURE)

    private fun repositoryFor(envelope: ApiEnvelope<PostDto>) =
        PostRepository(FakeApi(envelope), ErrorMapper(json))

    private fun viewModelFor(
        envelope: ApiEnvelope<PostDto>,
        writes: RecordingWrites,
    ): PostViewModel {
        val engagementRepository = com.us.android.core.engagement.data.EngagementRepository(
            UnusedEngagementApi(),
            ErrorMapper(json),
        )
        return PostViewModel(
            repository = repositoryFor(envelope),
            profiles = ProfileRepository(FakeProfileApi(), ErrorMapper(json)),
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
            engagement = EngagementStore(writes),
            engagementRepository = engagementRepository,
            savedStateHandle = SavedStateHandle(mapOf("postId" to POST_ID)),
        )
    }

    // ── Contract → domain ───────────────────────────────────────────────

    /**
     * Asserts the DOMAIN model, not that the bytes parsed. A DTO with a
     * default for every field deserializes any payload successfully, including
     * one where the mapping silently drops the value.
     */
    @Test
    fun `the captured viewer state reaches the domain model`() = runTest {
        val result = repositoryFor(decodeCaptured()).getPost(POST_ID)

        val post = (result as AppResult.Success).data
        assertThat(post.viewer.hasReacted).isTrue()
        assertThat(post.viewer.viewerReaction).isEqualTo("love")
        assertThat(post.viewer.isBookmarked).isTrue()
        assertThat(post.viewer.hasReposted).isTrue()
        assertThat(post.counts.likes).isEqualTo(1)
        assertThat(post.counts.reposts).isEqualTo(1)
    }

    /**
     * `viewer_reaction` is `omitempty`, so an unengaged viewer's payload omits
     * it entirely. Absent must mean "no reaction", not a parse failure.
     */
    @Test
    fun `an unengaged viewer decodes to all-false without viewer_reaction`() = runTest {
        val envelope: ApiEnvelope<PostDto> = json.decodeFromString(UNENGAGED_CAPTURE)

        val post = (repositoryFor(envelope).getPost(POST_ID) as AppResult.Success).data

        assertThat(post.viewer.hasReacted).isFalse()
        assertThat(post.viewer.viewerReaction).isNull()
        assertThat(post.viewer.isBookmarked).isFalse()
        assertThat(post.viewer.hasReposted).isFalse()
    }

    // ── First tap direction ─────────────────────────────────────────────

    @Test
    fun `the first tap on an already-reacted post sends DELETE`() = runTest {
        val writes = RecordingWrites()
        val viewModel = viewModelFor(decodeCaptured(), writes)
        runCurrent()

        viewModel.onReactToggle()
        runCurrent()

        assertThat(writes.calls).containsExactly("DELETE /reactions")
    }

    @Test
    fun `the first tap on an already-reposted post sends DELETE`() = runTest {
        val writes = RecordingWrites()
        val viewModel = viewModelFor(decodeCaptured(), writes)
        runCurrent()

        viewModel.onRepostToggle()
        runCurrent()

        assertThat(writes.calls).containsExactly("DELETE /repost")
    }

    @Test
    fun `the first tap on an already-bookmarked post sends DELETE`() = runTest {
        val writes = RecordingWrites()
        val viewModel = viewModelFor(decodeCaptured(), writes)
        runCurrent()

        viewModel.onBookmarkToggle()
        runCurrent()

        assertThat(writes.calls).containsExactly("DELETE /bookmark")
    }

    /** The opposite direction, so the tests above cannot pass by always DELETEing. */
    @Test
    fun `the first tap on an untouched post sends POST`() = runTest {
        val writes = RecordingWrites()
        val envelope: ApiEnvelope<PostDto> = json.decodeFromString(UNENGAGED_CAPTURE)
        val viewModel = viewModelFor(envelope, writes)
        runCurrent()

        viewModel.onReactToggle()
        runCurrent()
        viewModel.onRepostToggle()
        runCurrent()

        assertThat(writes.calls).containsExactly("POST /reactions", "POST /repost").inOrder()
    }

    private companion object {
        const val POST_ID = "a742c9a7-acb9-4751-a5b4-0f0a7b7763c8"

        /**
         * Verbatim, from post-service :8084 on 2026-08-21 with an engaged
         * viewer. Trimmed only of fields the client already models elsewhere.
         */
        const val ENGAGED_CAPTURE = """
        {"data":{"id":"a742c9a7-acb9-4751-a5b4-0f0a7b7763c8",
        "author_id":"2d373f48-6d0f-4a62-b439-51dee0b0ec2e",
        "text":"Followed image fixture for mixed feed","visibility":"public",
        "content_type":"post","is_pinned":false,"no_comments":false,"no_likes":false,
        "post_type":"image","app_origin":"postbook","review_status":"approved",
        "language":"en","comment_moderation":"none","comment_access":"everyone",
        "created_at":"2026-08-16T20:21:02.821823Z","updated_at":"2026-08-16T20:21:02.821823Z",
        "media":[{"media_id":"e4484a71-e26d-423a-a179-9ece7f977f11","kind":"image"}],
        "counts":{"likes":1,"comments":0},"view_count":0,
        "viewer_reaction":"love","has_reacted":true,"is_bookmarked":true,
        "repost_count":1,
        "viewer_repost":{"has_reposted":true,"repost_id":"9e290a66-8293-4da4-88d8-b4b75852805c","type":"plain","created_at":"2026-08-21T03:06:06Z"},
        "has_reposted":true,"is_repostable":true}}
        """

        /** The same endpoint for a viewer who has done nothing to the post. */
        const val UNENGAGED_CAPTURE = """
        {"data":{"id":"a742c9a7-acb9-4751-a5b4-0f0a7b7763c8",
        "author_id":"2d373f48-6d0f-4a62-b439-51dee0b0ec2e",
        "text":"Followed image fixture for mixed feed","visibility":"public",
        "content_type":"post","is_pinned":false,"no_comments":false,"no_likes":false,
        "post_type":"image","app_origin":"postbook","review_status":"approved",
        "language":"en","comment_moderation":"none","comment_access":"everyone",
        "created_at":"2026-08-16T20:21:02.821823Z","updated_at":"2026-08-16T20:21:02.821823Z",
        "media":[{"media_id":"e4484a71-e26d-423a-a179-9ece7f977f11","kind":"image"}],
        "counts":{"likes":0,"comments":0},"view_count":0,
        "has_reacted":false,"is_bookmarked":false,"repost_count":0,
        "has_reposted":false,"is_repostable":true}}
        """
    }
}
