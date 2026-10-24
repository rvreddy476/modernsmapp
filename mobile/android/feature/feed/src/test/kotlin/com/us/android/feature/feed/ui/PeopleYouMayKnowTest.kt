package com.us.android.feature.feed.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FollowStatus
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.feed.data.RecordingGraphApi
import com.us.android.feature.feed.data.followGraph
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/** People you may know on Home's For You (2026-10-24). */
class PeopleYouMayKnowTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private class FakeSource(var answer: AppResult<List<SuggestedPerson>>) : PeopleSuggestionsSource {
        val calls = mutableListOf<String>()
        override suspend fun people() = answer.also { calls += "people" }
        override suspend fun hide(userId: String) {
            calls += "hide($userId)"
        }
        override suspend fun followed(userId: String) {
            calls += "followed($userId)"
        }
        override suspend fun shown(people: List<SuggestedPerson>) {
            calls += "shown(${people.size})"
        }
    }

    private fun person(id: String) = SuggestedPerson(userId = id, name = "P$id", username = "p$id", avatarMediaId = null, line = "")

    // ── The page's place in the pager ────────────────────────────────────

    @Test
    fun `the page sits after the fourth post, the head counted, and only once four are loaded`() {
        assertThat(suggestionsPage(enabled = true, hasHead = false, itemCount = 10)).isEqualTo(4)
        assertThat(suggestionsPage(enabled = true, hasHead = true, itemCount = 10)).isEqualTo(5)
        assertThat(suggestionsPage(enabled = true, hasHead = false, itemCount = 3)).isNull()
        assertThat(suggestionsPage(enabled = false, hasHead = false, itemCount = 10)).isNull()
    }

    @Test
    fun `post pages skip the suggestions page`() {
        assertThat(postPageOf(3, suggestions = 4)).isEqualTo(3)
        assertThat(postPageOf(4, suggestions = 4)).isNull()
        assertThat(postPageOf(5, suggestions = 4)).isEqualTo(4)
        assertThat(postPageOf(7, suggestions = null)).isEqualTo(7)
    }

    @Test
    fun `the line under a name prefers mutual connections, then the reason, then the handle`() {
        assertThat(suggestionLine(1, "x", "ana")).isEqualTo("1 mutual connection")
        assertThat(suggestionLine(3, "x", "ana")).isEqualTo("3 mutual connections")
        assertThat(suggestionLine(0, " Follows you ", "ana")).isEqualTo("Follows you")
        assertThat(suggestionLine(0, "", "ana")).isEqualTo("@ana")
        assertThat(suggestionLine(0, "", "")).isEqualTo("Suggested for you")
    }

    // ── The ViewModel ────────────────────────────────────────────────────

    @Test
    fun `the people load once, and the viewer is never suggested to themselves`() = runTest {
        val source = FakeSource(AppResult.Success(listOf(person("a"), person("me"))))
        val vm = PeopleYouMayKnowViewModel(source, followGraph())
        advanceUntilIdle()

        assertThat(vm.state.value.loaded).isTrue()
        assertThat(vm.state.value.people.map { it.userId }).containsExactly("a")
    }

    @Test
    fun `a failed read is no page at all`() = runTest {
        val vm = PeopleYouMayKnowViewModel(FakeSource(AppResult.Failure(AppError.NoNetwork())), followGraph())
        advanceUntilIdle()

        assertThat(vm.state.value.people).isEmpty()
        assertThat(suggestionsPage(vm.state.value.people.isNotEmpty(), hasHead = false, itemCount = 10)).isNull()
    }

    @Test
    fun `Follow goes through the graph and tells the engine, Hide takes the person away`() = runTest {
        val graph = RecordingGraphApi()
        val source = FakeSource(AppResult.Success(listOf(person("a"), person("b"))))
        val vm = PeopleYouMayKnowViewModel(source, followGraph(graph))
        advanceUntilIdle()

        vm.follow("a")
        vm.hide("b")
        advanceUntilIdle()

        assertThat(graph.followRequests).containsExactly("a")
        assertThat(vm.edges.value["a"]).isEqualTo(FollowStatus.FOLLOWING)
        assertThat(source.calls).containsAtLeast("followed(a)", "hide(b)")
        assertThat(vm.state.value.people.map { it.userId }).containsExactly("a")
    }

    @Test
    fun `a refused follow is not reported to the engine`() = runTest {
        val graph = RecordingGraphApi(followFails = true)
        val source = FakeSource(AppResult.Success(listOf(person("a"))))
        val vm = PeopleYouMayKnowViewModel(source, followGraph(graph))
        advanceUntilIdle()

        vm.follow("a")
        advanceUntilIdle()

        assertThat(source.calls).doesNotContain("followed(a)")
    }

    @Test
    fun `the impression is sent once`() = runTest {
        val source = FakeSource(AppResult.Success(listOf(person("a"))))
        val vm = PeopleYouMayKnowViewModel(source, followGraph())
        advanceUntilIdle()

        vm.onShown()
        vm.onShown()
        advanceUntilIdle()

        assertThat(source.calls.count { it.startsWith("shown") }).isEqualTo(1)
    }
}
