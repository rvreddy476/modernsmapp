package com.us.android.feature.feed.ui.hashtag

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.hashtag.FollowedHashtag
import com.us.android.core.feed.data.hashtag.FollowedHashtagDto
import com.us.android.core.feed.data.hashtag.HashtagFollowApi
import com.us.android.core.feed.data.hashtag.HashtagFollowRepository
import com.us.android.core.feed.data.hashtag.HashtagFollowSession
import com.us.android.core.feed.data.hashtag.HashtagFollowStateDto
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * The hashtag page's Follow and the Followed hashtags list, against a fake
 * of post-service's follow routes (hashtag_follows_handler.go:16-27): the
 * server's answer is what the button shows, a refusal puts it back and says
 * why, and the list pages by cursor.
 */
class HashtagFollowViewModelsTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private class FakeApi : HashtagFollowApi {
        val following = mutableSetOf<String>()
        val calls = mutableListOf<String>()
        var refuse: String? = null
        var pages: Map<String?, Pair<List<String>, String?>> = emptyMap()

        private fun answer(call: String, tag: String, result: () -> Boolean): ApiEnvelope<HashtagFollowStateDto> {
            calls += call
            refuse?.let { code ->
                refuse = null
                return ApiEnvelope(error = ApiErrorBody(code = code))
            }
            return ApiEnvelope(HashtagFollowStateDto(tag = tag, displayName = "#$tag", following = result()))
        }

        override suspend fun state(tag: String) = answer("get:$tag", tag) { tag in following }
        override suspend fun follow(tag: String) = answer("post:$tag", tag) { following.add(tag).let { true } }
        override suspend fun unfollow(tag: String) = answer("delete:$tag", tag) { following.remove(tag).let { false } }
        override suspend fun followed(limit: Int, cursor: String?): ApiEnvelope<List<FollowedHashtagDto>> {
            calls += "list:${cursor ?: "-"}"
            refuse?.let { code ->
                refuse = null
                return ApiEnvelope(error = ApiErrorBody(code = code))
            }
            val (tags, next) = pages[cursor] ?: (emptyList<String>() to null)
            return ApiEnvelope(
                data = tags.map { FollowedHashtagDto(tag = it, displayName = "#$it") },
                meta = next?.let { ApiMeta(nextCursor = it) },
            )
        }
    }

    private val api = FakeApi()
    private val repository = HashtagFollowRepository(api, HashtagFollowSession(), ErrorMapper(NetworkModule.provideJson()))

    // ── The page's button ───────────────────────────────────────────────

    @Test
    fun `the button reads the state, then follows and unfollows by the server's answer`() = runTest {
        val viewModel = HashtagFollowViewModel(repository)

        viewModel.bind("#Android")
        assertThat(viewModel.state.value.following).isFalse()
        assertThat(viewModel.state.value.buttonLabel).isEqualTo("Follow")

        viewModel.toggle()
        assertThat(viewModel.state.value.following).isTrue()
        assertThat(viewModel.state.value.buttonLabel).isEqualTo("Following")

        viewModel.toggle()
        assertThat(viewModel.state.value.following).isFalse()
        assertThat(api.calls).containsExactly("get:android", "post:android", "delete:android").inOrder()
    }

    @Test
    fun `a refused follow puts the button back and says why`() = runTest {
        val viewModel = HashtagFollowViewModel(repository)
        viewModel.bind("android")
        api.refuse = "HASHTAG_FOLLOW_LIMIT"

        viewModel.toggle()

        assertThat(viewModel.state.value.following).isFalse()
        assertThat(viewModel.state.value.busy).isFalse()
        assertThat(viewModel.state.value.message?.text).isEqualTo("You can follow up to 500 hashtags. Unfollow one to follow another.")
    }

    @Test
    fun `a tag whose state cannot be read shows no button`() = runTest {
        val viewModel = HashtagFollowViewModel(repository)
        api.refuse = "HASHTAG_UNAVAILABLE"

        viewModel.bind("banned")
        viewModel.toggle()

        assertThat(viewModel.state.value.following).isNull()
        assertThat(api.calls).containsExactly("get:banned")
    }

    @Test
    fun `the page knows at once what the list already learned`() = runTest {
        api.following += "kotlin"
        api.pages = mapOf(null to (listOf("kotlin") to null))
        FollowedHashtagsViewModel(repository)
        api.refuse = "STORE_UNAVAILABLE"

        val page = HashtagFollowViewModel(repository)
        page.bind("kotlin")

        // The state read failed, but the session already knows: Following, not a guess.
        assertThat(page.state.value.following).isTrue()
    }

    // ── The list ────────────────────────────────────────────────────────

    @Test
    fun `the list pages by cursor and never lists a tag twice`() = runTest {
        api.pages = mapOf(
            null to (listOf("android", "kotlin") to "c2"),
            "c2" to (listOf("kotlin", "compose") to null),
        )
        val viewModel = FollowedHashtagsViewModel(repository)
        assertThat(viewModel.state.value.rows.map { it.tag }).containsExactly("android", "kotlin").inOrder()
        assertThat(viewModel.state.value.nextCursor).isEqualTo("c2")

        viewModel.loadMore()

        assertThat(viewModel.state.value.rows.map { it.tag }).containsExactly("android", "kotlin", "compose").inOrder()
        assertThat(viewModel.state.value.nextCursor).isNull()
        viewModel.loadMore()
        assertThat(api.calls).containsExactly("list:-", "list:c2").inOrder()
    }

    @Test
    fun `an unfollowed row stays, saying Follow, so it can be undone`() = runTest {
        api.following += "android"
        api.pages = mapOf(null to (listOf("android") to null))
        val viewModel = FollowedHashtagsViewModel(repository)

        viewModel.toggle("android")
        assertThat(viewModel.state.value.rows.single().following).isFalse()
        assertThat(followChangeLabel(viewModel.state.value.rows.single())).isEqualTo("Follow #android")

        viewModel.toggle("android")
        assertThat(viewModel.state.value.rows.single().following).isTrue()
        assertThat(api.calls).containsExactly("list:-", "delete:android", "post:android").inOrder()
    }

    @Test
    fun `a refused unfollow keeps the row followed and says why`() = runTest {
        api.pages = mapOf(null to (listOf("android") to null))
        val viewModel = FollowedHashtagsViewModel(repository)
        api.refuse = "STORE_UNAVAILABLE"

        viewModel.toggle("android")

        assertThat(viewModel.state.value.rows.single().following).isTrue()
        assertThat(viewModel.state.value.message?.text).isEqualTo("Hashtags are unavailable right now. Try again in a moment.")
    }

    @Test
    fun `an empty list and a failed list read differently`() = runTest {
        assertThat(FollowedHashtagsViewModel(repository).state.value.empty).isTrue()

        api.refuse = "INTERNAL_ERROR"
        val failed = FollowedHashtagsViewModel(repository).state.value
        assertThat(failed.empty).isFalse()
        assertThat(failed.loadError).isEqualTo("Your followed hashtags couldn't load. Try again.")
    }

    @Test
    fun `a page appends only the tags not already listed`() {
        val rows = listOf(FollowedHashtagRow("a", "#a"))
        assertThat(rows.plusPage(listOf(FollowedHashtag("a", "#a"), FollowedHashtag("b", "#b"))).map { it.tag })
            .containsExactly("a", "b").inOrder()
    }
}
