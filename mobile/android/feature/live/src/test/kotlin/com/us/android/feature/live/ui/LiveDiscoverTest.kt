package com.us.android.feature.live.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.live.data.LiveCategoryDto
import com.us.android.feature.live.data.LiveStreamDto
import com.us.android.feature.live.data.LiveUserStreams
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runCurrent
import org.junit.Rule
import org.junit.Test

/** Discover (For you, Following, categories, upcoming) and the creator's read-only centre. */
@OptIn(ExperimentalCoroutinesApi::class)
class LiveDiscoverTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val mainRule = MainDispatcherRule(dispatcher)

    private val api = FakeLiveApi()
    private val vms = HeldViewModels()
    private val errorMapper = ErrorMapper(NetworkModule.provideJson())

    private fun TestScope.hub(): LiveHubViewModel {
        val vm = vms.hold { LiveHubViewModel(api, errorMapper) }
        runCurrent()
        return vm
    }

    private fun TestScope.centre(ownId: String = "me"): LiveCreatorCentreViewModel {
        val vm = vms.hold { LiveCreatorCentreViewModel(api, errorMapper, FakeLiveFollows(ownId = ownId)) }
        runCurrent()
        return vm
    }

    @Test
    fun `For you is the plain list, with the category chips and what is coming up`() = liveTest(dispatcher, vms) {
        api.categories = listOf(LiveCategoryDto(slug = "music", label = "Music", liveCount = 1), LiveCategoryDto(slug = ""))
        api.upcoming = listOf(LiveStreamDto(id = "u1", title = "Friday", status = "scheduled"))

        val vm = hub()

        assertThat(api.listQueries).containsExactly("following=null category=null")
        assertThat(vm.state.value.streams.map { it.id }).containsExactly("s1")
        assertThat(vm.state.value.categories.map { it.slug }).containsExactly("music")
        assertThat(vm.state.value.upcoming.map { it.id }).containsExactly("u1")
        assertThat(vm.state.value.loading).isFalse()
    }

    @Test
    fun `Following asks for following=true, on the live and the upcoming lists`() = liveTest(dispatcher, vms) {
        val vm = hub()

        vm.onFeed(LiveHubViewModel.Feed.Following)
        runCurrent()

        assertThat(api.listQueries.last()).isEqualTo("following=true category=null")
        assertThat(api.upcomingQueries.last()).isEqualTo("following=true category=null")
        assertThat(vm.state.value.feed).isEqualTo(LiveHubViewModel.Feed.Following)
    }

    @Test
    fun `a category chip filters, and tapping it again shows every category`() = liveTest(dispatcher, vms) {
        val vm = hub()

        vm.onCategory("music")
        runCurrent()
        assertThat(api.listQueries.last()).isEqualTo("following=null category=music")
        assertThat(vm.state.value.category).isEqualTo("music")

        vm.onCategory("music")
        runCurrent()
        assertThat(api.listQueries.last()).isEqualTo("following=null category=null")
        assertThat(vm.state.value.category).isNull()
    }

    @Test
    fun `a failed live list is the screen's error, a failed extra is just left out`() = liveTest(dispatcher, vms) {
        api.failures["GET categories"] = httpError(500, "INTERNAL_ERROR")
        api.failures["GET upcoming"] = httpError(500, "INTERNAL_ERROR")
        val vm = hub()
        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.categories).isEmpty()

        api.failures["GET list"] = httpError(500, "INTERNAL_ERROR")
        vm.refresh()
        runCurrent()
        assertThat(vm.state.value.error).isEqualTo(LiveHubViewModel.LIST_FAILED_COPY)
    }

    @Test
    fun `the centre reads the creator's own upcoming and past streams, and recordings are the past ones with a recording`() =
        liveTest(dispatcher, vms) {
            api.userStreams = mapOf(
                LiveUserStreams.UPCOMING to listOf(LiveStreamDto(id = "u1", status = "scheduled")),
                LiveUserStreams.PAST to listOf(
                    LiveStreamDto(id = "p1", status = "ended", recordingPostId = "post-1"),
                    LiveStreamDto(id = "p2", status = "ended"),
                    LiveStreamDto(id = "p3", status = "ended", recordingUrl = "https://cdn.test/r.mp4"),
                ),
            )

            val vm = centre()

            assertThat(api.calls).containsAtLeast(
                "GET users/me/streams?status=upcoming",
                "GET users/me/streams?status=past",
            )
            assertThat(vm.state.value.rows.map { it.id }).containsExactly("u1")
            vm.onTab(LiveCreatorCentreViewModel.Tab.Past)
            assertThat(vm.state.value.rows.map { it.id }).containsExactly("p1", "p2", "p3").inOrder()
            vm.onTab(LiveCreatorCentreViewModel.Tab.Recordings)
            assertThat(vm.state.value.rows.map { it.id }).containsExactly("p1", "p3").inOrder()
        }

    @Test
    fun `the centre is read-only and only ever reads`() = liveTest(dispatcher, vms) {
        centre()

        assertThat(api.calls.filterNot { it.startsWith("GET ") }).isEmpty()
    }

    @Test
    fun `signed out, the centre asks nothing and says why`() = liveTest(dispatcher, vms) {
        val vm = centre(ownId = "")

        assertThat(api.calls).isEmpty()
        assertThat(vm.state.value.error).isEqualTo(LiveCreatorCentreViewModel.SIGNED_OUT_COPY)
    }
}
