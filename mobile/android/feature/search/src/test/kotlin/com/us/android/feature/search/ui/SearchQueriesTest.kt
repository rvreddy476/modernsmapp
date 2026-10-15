package com.us.android.feature.search.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.feature.search.data.RecentSearch
import com.us.android.feature.search.data.SearchHit
import com.us.android.feature.search.data.SearchSuggestion
import com.us.android.feature.search.navigation.SearchScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * The page's rules: a search runs on submit, not per keystroke; suggestions
 * wait two characters and 300 ms; the body follows the box.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class SearchQueriesTest {

    private val loaded = mutableListOf<SearchRequest>()
    private val suggested = mutableListOf<String>()
    private val user = SearchHit.User(id = "u1", username = "clee", displayName = "Clee", avatarUrl = null)

    private fun request(query: String, scope: SearchScope = SearchScope.TOP) = SearchRequest(query, scope)

    // ── The body ───────────────────────────────────────────────────────

    @Test
    fun `the body follows the box - empty, typing, searched`() {
        assertThat(SearchQueries.body(typed = "", submitted = "")).isEqualTo(SearchBody.Start)
        assertThat(SearchQueries.body(typed = "   ", submitted = "cats")).isEqualTo(SearchBody.Start)
        assertThat(SearchQueries.body(typed = "ca", submitted = "")).isEqualTo(SearchBody.Suggestions)
        assertThat(SearchQueries.body(typed = "cats ", submitted = "cats")).isEqualTo(SearchBody.Results)
        assertThat(SearchQueries.body(typed = "cats2", submitted = "cats")).isEqualTo(SearchBody.Suggestions)
    }

    @Test
    fun `autocomplete is not asked about the words already searched`() {
        assertThat(SearchQueries.suggestionText(typed = "cats", submitted = "cats")).isEmpty()
        assertThat(SearchQueries.suggestionText(typed = " cat ", submitted = "")).isEqualTo("cat")
    }

    // ── Searching: on submit ───────────────────────────────────────────

    @Test
    fun `nothing submitted is asked for nothing`() = runTest {
        val requests = MutableStateFlow(request(""))
        val states = mutableListOf<SearchResults>()
        backgroundScope.launch { SearchQueries.results(requests) { load(it) }.toList(states) }
        runCurrent()

        assertThat(loaded).isEmpty()
        assertThat(states).containsExactly(SearchResults.Idle)
    }

    @Test
    fun `a submitted search loads at once, no debounce, one letter is enough`() = runTest {
        val requests = MutableStateFlow(request("c"))
        val states = mutableListOf<SearchResults>()
        backgroundScope.launch { SearchQueries.results(requests) { load(it) }.toList(states) }
        runCurrent()

        assertThat(loaded).containsExactly(request("c"))
        assertThat(states).containsExactly(SearchResults.Loading, SearchResults.Loaded(request("c"), listOf(user)))
            .inOrder()
    }

    @Test
    fun `a change of tab is a new request for the same words`() = runTest {
        val requests = MutableStateFlow(request("cl"))
        backgroundScope.launch { SearchQueries.results(requests) { load(it) }.toList(mutableListOf()) }
        runCurrent()

        requests.value = request("cl", SearchScope.SOUNDS)
        runCurrent()

        assertThat(loaded.map { it.scope }).containsExactly(SearchScope.TOP, SearchScope.SOUNDS).inOrder()
    }

    @Test
    fun `the same request again is not asked twice, but a retry is`() = runTest {
        val requests = MutableStateFlow(request("cl"))
        backgroundScope.launch { SearchQueries.results(requests) { load(it) }.toList(mutableListOf()) }
        runCurrent()

        requests.value = request("cl")
        runCurrent()
        assertThat(loaded).hasSize(1)

        requests.value = request("cl").copy(attempt = 1)
        runCurrent()
        assertThat(loaded).hasSize(2)
    }

    @Test
    fun `a failure is the error state in words, with the request that failed`() = runTest {
        val requests = MutableStateFlow(request("offline"))
        val states = mutableListOf<SearchResults>()
        backgroundScope.launch { SearchQueries.results(requests) { load(it) }.toList(states) }
        runCurrent()

        val error = states.last() as SearchResults.Error
        assertThat(error.request).isEqualTo(request("offline"))
        assertThat(error.message).contains("offline")
    }

    // ── Suggestions: while typing ──────────────────────────────────────

    @Test
    fun `under two characters no suggestion is asked for`() = runTest {
        val texts = MutableStateFlow("c")
        val states = mutableListOf<List<SearchSuggestion>>()
        backgroundScope.launch { SearchQueries.suggestions(texts) { suggest(it) }.toList(states) }

        advanceTimeBy(SearchQueries.SUGGEST_DEBOUNCE_MILLIS * 3)

        assertThat(suggested).isEmpty()
        assertThat(states).containsExactly(emptyList<SearchSuggestion>())
    }

    @Test
    fun `suggestions wait the debounce, and a newer keystroke cancels the older one`() = runTest {
        val texts = MutableStateFlow("cl")
        val states = mutableListOf<List<SearchSuggestion>>()
        backgroundScope.launch { SearchQueries.suggestions(texts) { suggest(it) }.toList(states) }

        advanceTimeBy(SearchQueries.SUGGEST_DEBOUNCE_MILLIS / 2)
        texts.value = "cle"
        advanceTimeBy(SearchQueries.SUGGEST_DEBOUNCE_MILLIS / 2)
        assertThat(suggested).isEmpty()

        advanceTimeBy(SearchQueries.SUGGEST_DEBOUNCE_MILLIS)
        assertThat(suggested).containsExactly("cle")
        assertThat(states.last()).containsExactly(SearchSuggestion.Query("cle"))
    }

    // ── Recents ────────────────────────────────────────────────────────

    @Test
    fun `removing one recent keeps the rest, and a refused removal goes back where it was`() {
        val a = RecentSearch("1", "cats")
        val b = RecentSearch("2", "dogs")
        val c = RecentSearch("3", "birds")

        val removed = SearchQueries.without(listOf(a, b, c), "2")
        assertThat(removed).containsExactly(a, c).inOrder()

        assertThat(SearchQueries.restore(removed, b, index = 1)).containsExactly(a, b, c).inOrder()
        // Already back (a reload brought it): not doubled.
        assertThat(SearchQueries.restore(listOf(a, b, c), b, index = 1)).containsExactly(a, b, c).inOrder()
        // The list shrank meanwhile: it goes at the end, never out of range.
        assertThat(SearchQueries.restore(emptyList(), b, index = 5)).containsExactly(b)
    }

    private fun load(request: SearchRequest): AppResult<List<SearchHit>> {
        loaded += request
        return if (request.query == "offline") {
            AppResult.Failure(AppError.NoNetwork())
        } else {
            AppResult.Success(listOf(user))
        }
    }

    private fun suggest(text: String): List<SearchSuggestion> {
        suggested += text
        return listOf(SearchSuggestion.Query(text))
    }
}
