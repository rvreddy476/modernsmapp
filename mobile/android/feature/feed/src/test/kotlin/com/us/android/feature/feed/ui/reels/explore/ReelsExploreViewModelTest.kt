package com.us.android.feature.feed.ui.reels.explore

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.DiscoveryApi
import com.us.android.core.feed.data.DiscoveryRepository
import com.us.android.core.feed.data.EXPLORE_ALL
import com.us.android.core.feed.data.ExplorePage
import com.us.android.core.feed.data.FeedCategoryDto
import com.us.android.core.feed.data.TrendingPostsDto
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.ReelsEntry
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test
import java.io.IOException

/**
 * Reels Explore: the chips, the grid for a chip, and the rule the web pins —
 * one walk per viewer action, an empty page with a cursor waits for the
 * viewer ("Keep looking"), and a malformed category is never sent.
 */
class ReelsExploreViewModelTest {

    @get:Rule
    val mainDispatcher = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    /** Answers the reels surface from a script keyed by (category, cursor); unscripted is a failure. */
    private class ScriptedApi(
        private val pages: Map<Pair<String?, String?>, ApiEnvelope<List<FeedItemDto>>>,
        private val categories: List<FeedCategoryDto> = listOf(
            FeedCategoryDto(id = "comedy", slug = "comedy", label = "Comedy", kind = "all"),
            FeedCategoryDto(id = "music", slug = "music", label = "Music", kind = "all"),
        ),
    ) : DiscoveryApi {
        val asked = mutableListOf<Pair<String?, String?>>()

        override suspend fun categories(kind: String): ApiEnvelope<List<FeedCategoryDto>> = ApiEnvelope(data = categories)

        override suspend fun feed(
            surface: String,
            limit: Int,
            cursor: String?,
            category: String?,
            sort: String?,
        ): ApiEnvelope<List<FeedItemDto>> {
            check(surface == "reels")
            asked += category to cursor
            return pages[category to cursor] ?: throw IOException("unscripted $category/$cursor")
        }

        override suspend fun trending(contentType: String, limit: Int, cursor: String?): ApiEnvelope<TrendingPostsDto> =
            error("unused")
    }

    private fun page(vararg ids: String, next: String? = null) = ApiEnvelope(
        data = ids.map { FeedItemDto(id = it, postType = "video", contentType = "flick") },
        meta = next?.let { ApiMeta(nextCursor = it) },
    )

    private fun viewModel(api: ScriptedApi, entry: ReelsEntry = ReelsEntry()) = ReelsExploreViewModel(
        discovery = DiscoveryRepository(api, ErrorMapper(json)) { it },
        urlResolver = MediaUrlResolver(
            ApiConfig(
                baseUrl = "http://127.0.0.1:8080",
                wsBaseUrl = "ws://127.0.0.1:8093",
                clientVersion = "test",
                environment = "test",
                isDebug = true,
            ),
        ),
        reelsEntry = entry,
    )

    private fun item(id: String) = FeedItem(
        id = id,
        authorId = "a",
        author = FeedAuthor(id = "a", displayName = "Ada"),
        text = "",
        visibility = "public",
        feedContentType = "flick",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
    )

    @Test
    fun `opens on All with the chips and the first page`() {
        val api = ScriptedApi(mapOf((null to null) to page("r1", "r2", next = "c1")))

        val state = viewModel(api).state.value

        assertThat(state.chips.map { it.key }).containsExactly(EXPLORE_ALL, "comedy", "music").inOrder()
        assertThat(state.chips.first().label).isEqualTo("All")
        assertThat(state.tiles.map { it.id }).containsExactly("r1", "r2").inOrder()
        assertThat(exploreBody(state)).isEqualTo(ExploreBody.GRID)
        assertThat(state.wantsMore).isTrue()
        assertThat(api.asked).containsExactly(null to null)
    }

    @Test
    fun `a chip loads its own first page from the top, and the same chip again does nothing`() {
        val api = ScriptedApi(
            mapOf(
                (null to null) to page("r1", next = "c1"),
                ("music" to null) to page("m1"),
            ),
        )
        val vm = viewModel(api)

        vm.selectCategory("music")
        vm.selectCategory("music")

        assertThat(api.asked).containsExactly(null to null, "music" to null).inOrder()
        assertThat(vm.state.value.category).isEqualTo("music")
        assertThat(vm.state.value.categoryLabel).isEqualTo("Music")
        assertThat(vm.state.value.tiles.map { it.id }).containsExactly("m1")
        assertThat(vm.state.value.canLoadMore).isFalse()
    }

    @Test
    fun `an empty walk with a cursor asks the viewer to keep looking and never loads on its own`() {
        val api = ScriptedApi(
            mapOf(
                ("music" to null) to page(next = "w1"),
                ("music" to "w1") to page(next = "w2"),
                ("music" to "w2") to page(next = "w3"),
                ("music" to "w3") to page(next = "w4"),
                ("music" to "w4") to page("m1"),
                (null to null) to page("r1"),
            ),
        )
        val vm = viewModel(api)
        vm.selectCategory("music")

        val waiting = vm.state.value
        assertThat(exploreBody(waiting)).isEqualTo(ExploreBody.KEEP_LOOKING)
        assertThat(waiting.wantsMore).isFalse()
        assertThat(api.asked.count { it.first == "music" }).isEqualTo(4)

        vm.loadMore()

        assertThat(api.asked.last()).isEqualTo("music" to "w4")
        assertThat(exploreBody(vm.state.value)).isEqualTo(ExploreBody.GRID)
        assertThat(vm.state.value.tiles.map { it.id }).containsExactly("m1")
    }

    @Test
    fun `a malformed chip is refused without a request, and Show all goes back to All`() {
        val api = ScriptedApi(mapOf((null to null) to page("r1")))
        val vm = viewModel(api)

        vm.selectCategory("Not A Slug!")

        assertThat(exploreBody(vm.state.value)).isEqualTo(ExploreBody.INVALID)
        assertThat(api.asked).containsExactly(null to null)

        vm.showAll()
        assertThat(vm.state.value.category).isEqualTo(EXPLORE_ALL)
        assertThat(exploreBody(vm.state.value)).isEqualTo(ExploreBody.GRID)
    }

    @Test
    fun `the server's INVALID_CATEGORY reads as not available, not as a retry`() {
        val api = ScriptedApi(
            mapOf(
                (null to null) to page("r1"),
                ("gone" to null) to ApiEnvelope(error = ApiErrorBody(code = "INVALID_CATEGORY")),
            ),
        )
        val vm = viewModel(api)

        vm.selectCategory("gone")

        assertThat(vm.state.value.phase).isEqualTo(ExplorePhase.Invalid)
    }

    @Test
    fun `a failed first page is an error with retry, and a failed later page keeps the tiles`() {
        val api = ScriptedApi(mapOf((null to null) to page("r1", next = "c1")))
        val vm = viewModel(api)

        vm.loadMore()

        assertThat(vm.state.value.tiles.map { it.id }).containsExactly("r1")
        assertThat(vm.state.value.moreFailed).isTrue()
        assertThat(vm.state.value.wantsMore).isFalse()

        vm.selectCategory("comedy")
        assertThat(vm.state.value.phase).isInstanceOf(ExplorePhase.Failed::class.java)
        assertThat(exploreBody(vm.state.value)).isEqualTo(ExploreBody.FAILED)
    }

    @Test
    fun `a tile leaves its reel for the Reels tab`() {
        val entry = ReelsEntry()
        val vm = viewModel(ScriptedApi(mapOf((null to null) to page("r1"))), entry)

        vm.openReel("r1")

        assertThat(entry.requested.value).isEqualTo("r1")
    }

    @Test
    fun `the body follows the web's order of states`() {
        val ready = ReelsExploreState(phase = ExplorePhase.Ready)
        assertThat(exploreBody(ReelsExploreState())).isEqualTo(ExploreBody.LOADING)
        assertThat(exploreBody(ready.copy(phase = ExplorePhase.Invalid))).isEqualTo(ExploreBody.INVALID)
        assertThat(exploreBody(ready.copy(phase = ExplorePhase.Failed("x")))).isEqualTo(ExploreBody.FAILED)
        assertThat(exploreBody(ready)).isEqualTo(ExploreBody.EMPTY)
        assertThat(exploreBody(ready.copy(pages = listOf(ExplorePage(emptyList(), "c", 4))))).isEqualTo(ExploreBody.KEEP_LOOKING)
        assertThat(exploreBody(ready.copy(pages = listOf(ExplorePage(listOf(item("a")), null, 1))))).isEqualTo(ExploreBody.GRID)
    }
}
