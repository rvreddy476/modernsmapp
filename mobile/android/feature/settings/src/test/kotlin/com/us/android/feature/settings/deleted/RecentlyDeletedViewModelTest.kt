package com.us.android.feature.settings.deleted

import com.google.common.truth.Truth.assertThat
import com.us.android.core.engagement.data.DeletedMediaDto
import com.us.android.core.engagement.data.DeletedPostDto
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.engagement.data.PostLifecycleApi
import com.us.android.core.engagement.data.PostLifecycleRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Rule
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response

/** Cursor → the page it answers with, and the cursor after it. */
private typealias Pages = Map<String?, Pair<List<DeletedPostDto>, String?>>

/**
 * Settings › Recently deleted — the rules that would be invisible from the
 * screen's source:
 *
 *  - the list is the server's page, in its order, with the cursor kept for
 *    the next one; an empty page is the empty state, not an error;
 *  - Restore removes the row AND clears the id from the shared hidden set,
 *    which is what puts the post back in every feed without a refresh;
 *  - "the window has passed" (410, or 403 with a code) also removes the row —
 *    a Restore that cannot work must not be offered — while a transport
 *    failure keeps it and says so.
 */
class RecentlyDeletedViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }
    private val mapper = ErrorMapper(json)

    private class FakeApi(private val pages: Pages) : PostLifecycleApi {
        val restored = mutableListOf<String>()
        val listed = mutableListOf<String?>()
        var restoreStatus: Int? = null
        var offline = false

        override suspend fun deletePost(postId: String) = error("unused")

        override suspend fun restorePost(postId: String): ApiEnvelope<DeletedPostDto> {
            restored += postId
            if (offline) throw java.io.IOException("offline")
            restoreStatus?.let { status ->
                throw HttpException(
                    Response.error<Any>(
                        status,
                        """{"error":{"code":"RESTORE_WINDOW_PASSED","message":"too late"}}"""
                            .toResponseBody("application/json".toMediaType()),
                    ),
                )
            }
            return ApiEnvelope(data = DeletedPostDto(id = postId, text = "back"))
        }

        override suspend fun listDeleted(cursor: String?, limit: Int): ApiEnvelope<List<DeletedPostDto>> {
            listed += cursor
            if (offline) throw java.io.IOException("offline")
            val (items, next) = pages[cursor] ?: (emptyList<DeletedPostDto>() to null)
            return ApiEnvelope(data = items, meta = ApiMeta(nextCursor = next))
        }
    }

    private fun dto(id: String, text: String = "post $id") = DeletedPostDto(
        id = id,
        text = text,
        postType = "text",
        deletedAt = "2026-09-04T10:00:00Z",
        purgeAt = "2026-10-04T10:00:00Z",
    )

    private fun harness(
        pages: Pages = mapOf(null to (listOf(dto("p1"), dto("p2")) to null)),
        hidden: HiddenPosts = HiddenPosts(),
    ): Triple<RecentlyDeletedViewModel, FakeApi, HiddenPosts> {
        val api = FakeApi(pages)
        val viewModel = RecentlyDeletedViewModel(PostLifecycleRepository(api, mapper), hidden)
        return Triple(viewModel, api, hidden)
    }

    private fun RecentlyDeletedViewModel.content() = state.value as RecentlyDeletedUiState.Content

    // ── List ────────────────────────────────────────────────────────────

    @Test
    fun `the list is the server's page in its order`() = runTest {
        val (viewModel, api, _) = harness()

        assertThat(viewModel.content().posts.map { it.id }).containsExactly("p1", "p2").inOrder()
        assertThat(viewModel.content().hasMore).isFalse()
        assertThat(api.listed).containsExactly(null)
    }

    @Test
    fun `an empty page is the empty state, not an error`() = runTest {
        val (viewModel, _, _) = harness(pages = mapOf(null to (emptyList<DeletedPostDto>() to null)))

        assertThat(viewModel.state.value).isInstanceOf(RecentlyDeletedUiState.Content::class.java)
        assertThat(viewModel.content().isEmpty).isTrue()
    }

    @Test
    fun `a page that cannot be fetched is an error with a retry`() = runTest {
        val api = FakeApi(emptyMap()).apply { offline = true }
        val viewModel = RecentlyDeletedViewModel(PostLifecycleRepository(api, mapper), HiddenPosts())

        assertThat(viewModel.state.value).isInstanceOf(RecentlyDeletedUiState.Error::class.java)

        api.offline = false
        viewModel.load()

        assertThat(viewModel.state.value).isInstanceOf(RecentlyDeletedUiState.Content::class.java)
    }

    @Test
    fun `the next page appends behind the first and drops the cursor at the end`() = runTest {
        val (viewModel, api, _) = harness(
            pages = mapOf(
                null to (listOf(dto("p1")) to "c2"),
                "c2" to (listOf(dto("p2"), dto("p1")) to null),
            ),
        )
        assertThat(viewModel.content().hasMore).isTrue()

        viewModel.loadMore()

        assertThat(api.listed).containsExactly(null, "c2").inOrder()
        // A row the server repeated across the page boundary is not doubled.
        assertThat(viewModel.content().posts.map { it.id }).containsExactly("p1", "p2").inOrder()
        assertThat(viewModel.content().hasMore).isFalse()

        viewModel.loadMore()
        assertThat(api.listed).hasSize(2)
    }

    @Test
    fun `the row carries the still the server signed`() = runTest {
        val (viewModel, _, _) = harness(
            pages = mapOf(
                null to (
                    listOf(
                        dto("p1").copy(
                            media = listOf(
                                DeletedMediaDto(
                                    mediaId = "m1",
                                    kind = "image",
                                    variants = mapOf("720p" to "https://x/720", "thumb_150" to "https://x/thumb"),
                                ),
                            ),
                        ),
                    ) to null
                    ),
            ),
        )

        assertThat(viewModel.content().posts.single().thumbnailUrl).isEqualTo("https://x/thumb")
    }

    // ── Restore ─────────────────────────────────────────────────────────

    @Test
    fun `restore removes the row and clears the post from the hidden set`() = runTest {
        val hidden = HiddenPosts().apply {
            hidePost("p1")
            hidePost("p2")
        }
        val (viewModel, api, _) = harness(hidden = hidden)

        viewModel.restore("p1")

        assertThat(api.restored).containsExactly("p1")
        assertThat(viewModel.content().posts.map { it.id }).containsExactly("p2")
        assertThat(viewModel.content().restoring).isEmpty()
        assertThat(viewModel.content().message).isNull()
        assertThat(hidden.state.value.postIds).containsExactly("p2")
    }

    @Test
    fun `restore after the window has passed removes the row and says so`() = runTest {
        val hidden = HiddenPosts().apply { hidePost("p1") }
        val (viewModel, api, _) = harness(hidden = hidden)
        api.restoreStatus = 410

        viewModel.restore("p1")

        assertThat(viewModel.content().posts.map { it.id }).containsExactly("p2")
        assertThat(viewModel.content().message).isEqualTo("That post can no longer be restored.")
        // The server did not bring it back, so neither does the client.
        assertThat(hidden.state.value.postIds).containsExactly("p1")
    }

    @Test
    fun `a 403 with a code on the viewer's own list reads as the window having passed`() = runTest {
        val (viewModel, api, _) = harness()
        api.restoreStatus = 403

        viewModel.restore("p2")

        assertThat(viewModel.content().posts.map { it.id }).containsExactly("p1")
        assertThat(viewModel.content().message).isEqualTo("That post can no longer be restored.")
    }

    @Test
    fun `a restore that cannot reach the server keeps the row and says so`() = runTest {
        val hidden = HiddenPosts().apply { hidePost("p1") }
        val (viewModel, api, _) = harness(hidden = hidden)
        api.offline = true

        viewModel.restore("p1")

        assertThat(viewModel.content().posts.map { it.id }).containsExactly("p1", "p2").inOrder()
        assertThat(viewModel.content().restoring).isEmpty()
        assertThat(viewModel.content().message).contains("offline")
        assertThat(hidden.state.value.postIds).containsExactly("p1")

        viewModel.dismissMessage()
        assertThat(viewModel.content().message).isNull()
    }

    @Test
    fun `restoring the last row leaves the empty state behind`() = runTest {
        val (viewModel, _, _) = harness(pages = mapOf(null to (listOf(dto("p1")) to null)))

        viewModel.restore("p1")

        assertThat(viewModel.content().isEmpty).isTrue()
    }
}
