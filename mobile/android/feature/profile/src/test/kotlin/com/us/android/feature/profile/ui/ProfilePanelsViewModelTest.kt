package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.Paged
import com.us.android.core.profile.data.ProfileAbout
import com.us.android.core.profile.data.ProfilePanelsSource
import com.us.android.core.profile.data.PublicLink
import com.us.android.core.profile.data.RepostRow
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

class ProfilePanelsViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private class FakePanels : ProfilePanelsSource {
        val calls = mutableListOf<String>()
        var about: AppResult<ProfileAbout> = AppResult.Success(ProfileAbout(emptyList(), listOf(PublicLink("site", "https://a.dev"))))
        val pages = ArrayDeque<AppResult<Paged<RepostRow>>>()

        override suspend fun about(userId: String): AppResult<ProfileAbout> {
            calls += "about($userId)"
            return about
        }

        override suspend fun reposts(userId: String, cursor: String?): AppResult<Paged<RepostRow>> {
            calls += "reposts($userId,$cursor)"
            return pages.removeFirstOrNull() ?: AppResult.Success(Paged(emptyList(), null))
        }
    }

    private fun repost(id: String) = RepostRow(id, "", "p-$id", "post", "text", "Ana", "")

    @Test
    fun `each panel reads once when opened, and again only on a pull`() = runTest {
        val panels = FakePanels()
        val vm = ProfilePanelsViewModel(panels)

        vm.open("u1", ProfileGridTab.ABOUT)
        vm.open("u1", ProfileGridTab.ABOUT)
        assertThat(panels.calls).containsExactly("about(u1)")
        assertThat(vm.state.value.about.data?.links).containsExactly(PublicLink("site", "https://a.dev"))

        vm.open("u1", ProfileGridTab.ABOUT, force = true)
        assertThat(panels.calls).containsExactly("about(u1)", "about(u1)")
    }

    @Test
    fun `a failed About says so and keeps nothing invented`() = runTest {
        val panels = FakePanels().apply { about = AppResult.Failure(AppError.NotFound()) }
        val vm = ProfilePanelsViewModel(panels)

        vm.open("u1", ProfileGridTab.ABOUT)

        assertThat(vm.state.value.about.data).isNull()
        assertThat(vm.state.value.about.error).isNotNull()
    }

    @Test
    fun `the People tab is Followers, Following and, on the own profile, Blocked`() {
        assertThat(peopleRows(isOwn = true, canOpenBlocked = true))
            .containsExactly(PeopleRow.FOLLOWERS, PeopleRow.FOLLOWING, PeopleRow.BLOCKED).inOrder()
        assertThat(peopleRows(isOwn = false, canOpenBlocked = true))
            .containsExactly(PeopleRow.FOLLOWERS, PeopleRow.FOLLOWING).inOrder()
        assertThat(peopleRows(isOwn = true, canOpenBlocked = false))
            .containsExactly(PeopleRow.FOLLOWERS, PeopleRow.FOLLOWING).inOrder()
        assertThat(PeopleRow.entries.map { it.label }).containsExactly("Followers", "Following", "Blocked").inOrder()
    }

    @Test
    fun `the People tab reads nothing of its own and no connections list survives`() = runTest {
        val panels = FakePanels()
        val vm = ProfilePanelsViewModel(panels)

        vm.open("u1", ProfileGridTab.PEOPLE)

        assertThat(panels.calls).isEmpty()
        assertThat(ProfileGridTab.entries.map { it.label }).doesNotContain("Connections")
        assertThat(ProfilePanelsUiState::class.java.declaredFields.map { it.name }).doesNotContain("connections")
    }

    @Test
    fun `reposts page through the cursor without repeating a row`() = runTest {
        val panels = FakePanels().apply {
            pages += AppResult.Success(Paged(listOf(repost("r1"), repost("r2")), "c2"))
            pages += AppResult.Success(Paged(listOf(repost("r2"), repost("r3")), null))
        }
        val vm = ProfilePanelsViewModel(panels)

        vm.open("u1", ProfileGridTab.REPOSTS)
        vm.loadMoreReposts()
        vm.loadMoreReposts()

        assertThat(panels.calls).containsExactly("reposts(u1,null)", "reposts(u1,c2)").inOrder()
        assertThat(vm.state.value.reposts.rows.map { it.repostId }).containsExactly("r1", "r2", "r3").inOrder()
        assertThat(vm.state.value.reposts.nextCursor).isNull()
    }

    @Test
    fun `another account starts the panels over`() = runTest {
        val panels = FakePanels()
        val vm = ProfilePanelsViewModel(panels)

        vm.open("u1", ProfileGridTab.ABOUT)
        vm.open("u2", ProfileGridTab.ABOUT)

        assertThat(panels.calls).containsExactly("about(u1)", "about(u2)").inOrder()
    }

    @Test
    fun `the panel tabs come after the author tabs for everyone, and only where the host has them`() {
        assertThat(gridTabsFor(isOwn = false, panels = true)).containsExactly(
            ProfileGridTab.POSTS,
            ProfileGridTab.REELS,
            ProfileGridTab.VIDEOS,
            ProfileGridTab.ABOUT,
            ProfileGridTab.PEOPLE,
            ProfileGridTab.REPOSTS,
        ).inOrder()
        assertThat(gridTabsFor(isOwn = true, panels = true).takeLast(2)).containsExactly(ProfileGridTab.SAVED, ProfileGridTab.LIKED)
        assertThat(gridTabsFor(isOwn = false)).containsNoneOf(ProfileGridTab.ABOUT, ProfileGridTab.PEOPLE, ProfileGridTab.REPOSTS)
        assertThat(ProfileGridTab.REPOSTS.panel).isTrue()
        assertThat(ProfileGridTab.REPOSTS.sortable).isFalse()
        assertThat(ProfileGridTab.POSTS.panel).isFalse()
    }

    @Test
    fun `only an https link leaves the app from a profile`() {
        assertThat(isOpenableLink("https://ana.dev")).isTrue()
        assertThat(isOpenableLink("  https://ana.dev/x  ")).isTrue()
        assertThat(isOpenableLink("http://ana.dev")).isFalse()
        assertThat(isOpenableLink("javascript:alert(1)")).isFalse()
        assertThat(isOpenableLink("https://")).isFalse()
        assertThat(isOpenableLink("https://a b.dev")).isFalse()
    }

    @Test
    fun `joined reads the month and year, and nothing for a bad date`() {
        assertThat(joinedLine("2026-08-16T17:53:05Z")).isEqualTo("Joined August 2026")
        assertThat(joinedLine("")).isNull()
    }

    @Test
    fun `the repost line says quoted or reposted, and whose`() {
        assertThat(repostByline(repost("r1"))).isEqualTo("Reposted Ana's post")
        assertThat(repostByline(repost("r1").copy(quoteText = "so true"))).isEqualTo("Quoted Ana's post")
        assertThat(repostByline(repost("r1").copy(originalAuthor = ""))).isEqualTo("Reposted a post")
    }
}
