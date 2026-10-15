package com.us.android.feature.live.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.listApiCall
import com.us.android.feature.live.data.LiveApi
import com.us.android.feature.live.data.LiveCategoryDto
import com.us.android.feature.live.data.LiveStreamDto
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Discover LIVE (T-30, 2026-10-15): For you or Following, a category chip
 * row, who is live now, and what is coming up — every list from a route
 * live-service-v2 already serves:
 *
 *   GET /v1/livestream/streams?following=true&category=<slug>   live now
 *   GET /v1/livestream/categories/live                         the chips
 *   GET /v1/livestream/streams/upcoming?following=&category=   upcoming
 *
 * The live list is the screen: if it fails, the screen says so. The chips
 * and the upcoming row are extras; a failure there just leaves them out.
 */
@HiltViewModel
class LiveHubViewModel @Inject constructor(
    private val api: LiveApi,
    private val errorMapper: ErrorMapper,
) : ViewModel() {

    /** The two discover feeds. */
    enum class Feed(val label: String) {
        ForYou("For you"),
        Following("Following"),
    }

    data class UiState(
        val loading: Boolean = true,
        val feed: Feed = Feed.ForYou,
        /** The selected category's slug; null is every category. */
        val category: String? = null,
        val categories: List<LiveCategoryDto> = emptyList(),
        val streams: List<LiveStreamDto> = emptyList(),
        val upcoming: List<LiveStreamDto> = emptyList(),
        val error: String? = null,
    )

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state.asStateFlow()

    private var loadJob: Job? = null

    init {
        refresh()
    }

    fun onFeed(feed: Feed) {
        if (feed == _state.value.feed) return
        _state.update { it.copy(feed = feed) }
        refresh()
    }

    /** A chip. Tapping the selected one again goes back to every category. */
    fun onCategory(slug: String?) {
        val next = slug?.takeIf { it.isNotBlank() && it != _state.value.category }
        _state.update { it.copy(category = next) }
        refresh()
    }

    fun refresh() {
        loadJob?.cancel()
        _state.update { it.copy(loading = true, error = null) }
        val current = _state.value
        // Sent only as `true`: an absent parameter is the plain list.
        val following = if (current.feed == Feed.Following) true else null
        val category = current.category
        loadJob = viewModelScope.launch {
            val live = async {
                listApiCall(errorMapper) { api.listLiveNow(following = following, category = category) }
            }
            val chips = async { listApiCall(errorMapper) { api.liveCategories() } }
            val upcoming = async {
                listApiCall(errorMapper) { api.listUpcoming(following = following, category = category) }
            }
            val categories = (chips.await() as? AppResult.Success)?.data.orEmpty().filter { it.slug.isNotBlank() }
            val soon = (upcoming.await() as? AppResult.Success)?.data.orEmpty().filter { it.id.isNotBlank() }
            when (val result = live.await()) {
                is AppResult.Success -> _state.update {
                    it.copy(
                        loading = false,
                        streams = result.data.filter { row -> row.id.isNotBlank() },
                        categories = categories,
                        upcoming = soon,
                    )
                }
                is AppResult.Failure -> _state.update {
                    it.copy(loading = false, error = LIST_FAILED_COPY, categories = categories, upcoming = soon)
                }
            }
        }
    }

    internal companion object {
        const val LIST_FAILED_COPY = "Couldn't load live streams."
    }
}
