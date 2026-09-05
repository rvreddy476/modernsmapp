package com.us.android.feature.search.ui

import com.us.android.core.common.result.AppResult
import com.us.android.feature.search.data.SearchHit
import com.us.android.feature.search.data.SearchRepository
import com.us.android.feature.search.navigation.SearchScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.transformLatest

/**
 * What the page is asking for: the trimmed text and the chip. [attempt] is
 * bumped by Retry so the same words are asked for again — a request that
 * failed must not be swallowed as a duplicate of itself.
 */
data class SearchRequest(val query: String, val scope: SearchScope, val attempt: Int = 0)

/** What the results area shows. */
sealed interface SearchResults {
    /** Nothing typed yet, or fewer than [SearchQueries.MIN_CHARS]: the recents, or the hint. */
    data object Idle : SearchResults
    data object Loading : SearchResults
    data class Loaded(val request: SearchRequest, val hits: List<SearchHit>) : SearchResults
    data class Error(val request: SearchRequest, val message: String) : SearchResults
}

/**
 * The typing rules (founder, 2026-09-05): nothing is asked for under two
 * characters, and a request waits 300 ms for the next keystroke — a newer
 * request cancels the older one before it is sent, so a fast typist costs
 * one call, not one per letter. Pure over a flow of requests, so the
 * timing is a test with a virtual clock.
 */
object SearchQueries {
    const val MIN_CHARS = 2
    const val DEBOUNCE_MILLIS = 300L

    @OptIn(ExperimentalCoroutinesApi::class)
    fun results(
        requests: Flow<SearchRequest>,
        load: suspend (SearchRequest) -> AppResult<List<SearchHit>>,
    ): Flow<SearchResults> = requests
        .distinctUntilChanged()
        .transformLatest { request ->
            if (request.query.length < MIN_CHARS) {
                emit(SearchResults.Idle)
                return@transformLatest
            }
            delay(DEBOUNCE_MILLIS)
            emit(SearchResults.Loading)
            emit(
                when (val result = load(request)) {
                    is AppResult.Success -> SearchResults.Loaded(request, result.data)
                    is AppResult.Failure -> SearchResults.Error(request, SearchRepository.errorMessage(result.error))
                },
            )
        }
}
