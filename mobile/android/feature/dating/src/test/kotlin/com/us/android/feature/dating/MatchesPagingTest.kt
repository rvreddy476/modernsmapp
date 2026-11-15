package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.home.ListState
import com.us.android.feature.dating.home.MatchUi
import com.us.android.feature.dating.home.MatchesViewModel
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * The Matches tab reads a page at a time (dating-service 2026-11-10,
 * `limit` + `cursor`, `meta.next_cursor` while another page follows): the
 * active bucket to its end, then the quiet one, each row once.
 */
class MatchesPagingTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession().apply { setProfile(profile()) }
    private val urls = photoUrls()
    private val limit = DatingRepository.MATCH_PAGE_LIMIT

    private fun active(n: Int) = match("a$n", "user-a$n")
    private fun quiet(n: Int) = match("q$n", "user-q$n").copy(status = "quiet")

    private fun ids(vm: MatchesViewModel): List<String> =
        (vm.state.value as ListState.Items<MatchUi>).items.map { it.matchId }

    @Test
    fun `the first page is a paged read of the active bucket, and the rest follow on loadMore`() = runTest {
        api.matches = (1..limit + 5).map(::active) + listOf(quiet(1))

        val vm = MatchesViewModel(api.repository(), session, urls)

        assertThat(api.matchPageReads).containsExactly("active:$limit:null")
        assertThat(ids(vm)).hasSize(limit)
        assertThat(vm.hasMore.value).isTrue()

        vm.loadMore()

        // The active bucket ended on this page, so its quiet matches come in the same read.
        assertThat(api.matchPageReads)
            .containsExactly("active:$limit:null", "active:$limit:$limit", "quiet:$limit:null")
            .inOrder()
        assertThat(ids(vm)).hasSize(limit + 6)
        assertThat(ids(vm).last()).isEqualTo("q1")
        assertThat(vm.hasMore.value).isFalse()
    }

    @Test
    fun `a short active list shows its quiet matches at once, and nothing follows`() = runTest {
        api.matches = listOf(active(1), quiet(1))

        val vm = MatchesViewModel(api.repository(), session, urls)

        assertThat(ids(vm)).containsExactly("a1", "q1").inOrder()
        assertThat(vm.hasMore.value).isFalse()
        vm.loadMore()
        assertThat(api.matchPageReads).hasSize(2)
    }

    @Test
    fun `a refresh starts from the top and replaces the rows`() = runTest {
        api.matches = (1..limit + 1).map(::active)
        val vm = MatchesViewModel(api.repository(), session, urls)
        vm.loadMore()
        assertThat(ids(vm)).hasSize(limit + 1)

        api.matches = listOf(active(9))
        vm.refresh()

        assertThat(ids(vm)).containsExactly("a9")
        assertThat(api.matchPageReads.last()).isEqualTo("quiet:$limit:null")
    }

    @Test
    fun `a later page that fails keeps the rows shown and is offered again`() = runTest {
        api.matches = (1..limit + 1).map(::active)
        val vm = MatchesViewModel(api.repository(), session, urls)
        api.matchesBucketResponse = { refused(400, "INVALID_CURSOR") }

        vm.loadMore()

        assertThat(ids(vm)).hasSize(limit)
        assertThat(vm.hasMore.value).isTrue()

        api.matchesBucketResponse = null
        vm.loadMore()

        assertThat(ids(vm)).hasSize(limit + 1)
    }
}
