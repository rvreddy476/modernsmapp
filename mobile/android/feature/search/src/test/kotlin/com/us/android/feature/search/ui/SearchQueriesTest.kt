package com.us.android.feature.search.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.feature.search.data.SearchHit
import com.us.android.feature.search.navigation.SearchScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runTest
import org.junit.Test

/** The typing rules: two characters before anything is asked for, and a 300 ms wait that a newer keystroke resets. */
@OptIn(ExperimentalCoroutinesApi::class)
class SearchQueriesTest {

    private val loaded = mutableListOf<SearchRequest>()
    private val user = SearchHit.User(id = "u1", username = "clee", displayName = "Clee", avatarUrl = null)

    private fun request(query: String) = SearchRequest(query, SearchScope.USERS)

    @Test
    fun `under two characters nothing is asked for`() = runTest {
        val requests = MutableStateFlow(request("c"))
        val states = mutableListOf<SearchResults>()
        backgroundScope.launch { SearchQueries.results(requests) { load(it) }.toList(states) }

        advanceTimeBy(SearchQueries.DEBOUNCE_MILLIS * 3)

        assertThat(loaded).isEmpty()
        assertThat(states).containsExactly(SearchResults.Idle)
    }

    @Test
    fun `a request waits the debounce, then loads once`() = runTest {
        val requests = MutableStateFlow(request("cl"))
        val states = mutableListOf<SearchResults>()
        backgroundScope.launch { SearchQueries.results(requests) { load(it) }.toList(states) }

        advanceTimeBy(SearchQueries.DEBOUNCE_MILLIS - 1)
        assertThat(loaded).isEmpty()

        advanceTimeBy(2)
        assertThat(loaded).containsExactly(request("cl"))
        assertThat(states.last()).isEqualTo(SearchResults.Loaded(request("cl"), listOf(user)))
    }

    @Test
    fun `a newer keystroke inside the window cancels the older request`() = runTest {
        val requests = MutableStateFlow(request("cl"))
        val states = mutableListOf<SearchResults>()
        backgroundScope.launch { SearchQueries.results(requests) { load(it) }.toList(states) }

        advanceTimeBy(SearchQueries.DEBOUNCE_MILLIS / 2)
        requests.value = request("cle")
        advanceTimeBy(SearchQueries.DEBOUNCE_MILLIS / 2)
        assertThat(loaded).isEmpty()

        advanceTimeBy(SearchQueries.DEBOUNCE_MILLIS)
        assertThat(loaded).containsExactly(request("cle"))
    }

    @Test
    fun `a change of chip is a new request for the same words`() = runTest {
        val requests = MutableStateFlow(request("cl"))
        val states = mutableListOf<SearchResults>()
        backgroundScope.launch { SearchQueries.results(requests) { load(it) }.toList(states) }
        advanceTimeBy(SearchQueries.DEBOUNCE_MILLIS + 1)

        requests.value = SearchRequest("cl", SearchScope.POSTS)
        advanceTimeBy(SearchQueries.DEBOUNCE_MILLIS + 1)

        assertThat(loaded.map { it.scope }).containsExactly(SearchScope.USERS, SearchScope.POSTS).inOrder()
    }

    @Test
    fun `the same request again is not asked twice, but a retry is`() = runTest {
        val requests = MutableStateFlow(request("cl"))
        val states = mutableListOf<SearchResults>()
        backgroundScope.launch { SearchQueries.results(requests) { load(it) }.toList(states) }
        advanceTimeBy(SearchQueries.DEBOUNCE_MILLIS + 1)

        requests.value = request("cl")
        advanceTimeBy(SearchQueries.DEBOUNCE_MILLIS + 1)
        assertThat(loaded).hasSize(1)

        requests.value = request("cl").copy(attempt = 1)
        advanceTimeBy(SearchQueries.DEBOUNCE_MILLIS + 1)
        assertThat(loaded).hasSize(2)
    }

    @Test
    fun `a failure is the error state in words, with the request that failed`() = runTest {
        val requests = MutableStateFlow(request("offline"))
        val states = mutableListOf<SearchResults>()
        backgroundScope.launch { SearchQueries.results(requests) { load(it) }.toList(states) }

        advanceTimeBy(SearchQueries.DEBOUNCE_MILLIS + 1)

        val error = states.last() as SearchResults.Error
        assertThat(error.request).isEqualTo(request("offline"))
        assertThat(error.message).contains("offline")
    }

    private fun load(request: SearchRequest): AppResult<List<SearchHit>> {
        loaded += request
        return if (request.query == "offline") {
            AppResult.Failure(AppError.NoNetwork())
        } else {
            AppResult.Success(listOf(user))
        }
    }
}
