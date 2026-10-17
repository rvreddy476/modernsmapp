package com.us.android.feature.search.ui

import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FollowStatus
import com.us.android.feature.search.data.RecentSearch
import com.us.android.feature.search.data.SearchHit
import com.us.android.feature.search.data.SearchRepository
import com.us.android.feature.search.data.SearchSuggestion
import com.us.android.feature.search.navigation.SearchScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.transformLatest

/**
 * What the page is asking for: the SUBMITTED words and the tab. [attempt]
 * is bumped by Retry and by submitting the same words again, so a request
 * that failed is never swallowed as a duplicate of itself.
 */
data class SearchRequest(val query: String, val scope: SearchScope, val attempt: Int = 0)

/** The control at the end of a people row. */
enum class UserFollowButton { HIDDEN, FOLLOW, FOLLOWING, REQUESTED }

/** What the results area shows. */
sealed interface SearchResults {
    /** Nothing submitted yet. */
    data object Idle : SearchResults
    data object Loading : SearchResults
    data class Loaded(val request: SearchRequest, val hits: List<SearchHit>) : SearchResults
    data class Error(val request: SearchRequest, val message: String) : SearchResults
}

/** Which of the page's three bodies is up. */
enum class SearchBody {
    /** The box is empty: recent searches (signed in) and popular searches. */
    Start,

    /** Something is typed that has not been searched: suggestions. */
    Suggestions,

    /** The words in the box are the ones searched: the tab's results. */
    Results,
}

/**
 * The search page's rules (T-16, 2026-10-15). A search runs on SUBMIT —
 * the keyboard's Search, a recent, a popular search or a suggestion — not
 * per keystroke: the server records a signed-in first page as a search
 * made, and a keystroke is not one. While typing, autocomplete offers
 * suggestions: nothing under [MIN_SUGGEST_CHARS] characters, and each
 * waits [SUGGEST_DEBOUNCE_MILLIS] for the next keystroke, which cancels it.
 * Pure over flows, so the timing is a test with a virtual clock.
 */
object SearchQueries {
    const val MIN_SUGGEST_CHARS = 2
    const val SUGGEST_DEBOUNCE_MILLIS = 300L

    /**
     * [results] without the signed-in viewer's own account (2026-10-17):
     * search-service returns everyone matching the words, the viewer
     * included, and a row offering to follow yourself is noise.
     */
    fun withoutSelf(results: SearchResults, ownUserId: String): SearchResults =
        if (results !is SearchResults.Loaded || ownUserId.isBlank()) {
            results
        } else {
            results.copy(hits = results.hits.filterNot { it is SearchHit.User && it.id == ownUserId })
        }

    /**
     * The control at the end of a people row (2026-10-17): Follow, or
     * Following / Requested once the viewer has, so the row says what is
     * true rather than dropping the button. Nothing on the viewer's own row.
     * A person whose edge is not known yet shows Follow: tapping it on
     * someone already followed lands on Following, and the button never
     * vanishes from under a reader the way a hidden-until-known one did.
     */
    fun followButton(ownUserId: String, userId: String, edge: FollowStatus?): UserFollowButton = when {
        userId.isBlank() || userId == ownUserId -> UserFollowButton.HIDDEN
        edge == FollowStatus.FOLLOWING -> UserFollowButton.FOLLOWING
        edge == FollowStatus.REQUESTED -> UserFollowButton.REQUESTED
        else -> UserFollowButton.FOLLOW
    }

    /** The body for what is [typed] in the box, given the words last [submitted]. */
    fun body(typed: String, submitted: String): SearchBody = when {
        typed.isBlank() -> SearchBody.Start
        typed.trim() != submitted -> SearchBody.Suggestions
        else -> SearchBody.Results
    }

    /** The text autocomplete should be asked about: none once the box holds the searched words. */
    fun suggestionText(typed: String, submitted: String): String =
        typed.trim().takeUnless { it == submitted }.orEmpty()

    /** The submitted requests, each loaded at once; a newer request cancels an older one still in flight. */
    @OptIn(ExperimentalCoroutinesApi::class)
    fun results(
        requests: Flow<SearchRequest>,
        load: suspend (SearchRequest) -> AppResult<List<SearchHit>>,
    ): Flow<SearchResults> = requests
        .distinctUntilChanged()
        .transformLatest { request ->
            if (request.query.isBlank()) {
                emit(SearchResults.Idle)
                return@transformLatest
            }
            emit(SearchResults.Loading)
            emit(
                when (val result = load(request)) {
                    is AppResult.Success -> SearchResults.Loaded(request, result.data)
                    is AppResult.Failure -> SearchResults.Error(request, SearchRepository.errorMessage(result.error))
                },
            )
        }

    /** Suggestions for the text being typed: debounced, and nothing asked for under two characters. */
    @OptIn(ExperimentalCoroutinesApi::class)
    fun suggestions(
        texts: Flow<String>,
        load: suspend (String) -> List<SearchSuggestion>,
    ): Flow<List<SearchSuggestion>> = texts
        .map { it.trim() }
        .distinctUntilChanged()
        .transformLatest { text ->
            if (text.length < MIN_SUGGEST_CHARS) {
                emit(emptyList())
                return@transformLatest
            }
            delay(SUGGEST_DEBOUNCE_MILLIS)
            emit(load(text))
        }

    /** [list] without the entry [id] — what "remove" shows before the server confirms it. */
    fun without(list: List<RecentSearch>, id: String): List<RecentSearch> = list.filterNot { it.id == id }

    /** Puts [item] back where it was, for a removal the server refused. */
    fun restore(list: List<RecentSearch>, item: RecentSearch, index: Int): List<RecentSearch> =
        if (list.any { it.id == item.id }) {
            list
        } else {
            list.toMutableList().apply { add(index.coerceIn(0, size), item) }
        }
}
