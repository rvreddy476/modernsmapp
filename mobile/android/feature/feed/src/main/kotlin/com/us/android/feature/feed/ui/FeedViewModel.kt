package com.us.android.feature.feed.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedSurface
import com.us.android.feature.feed.data.FeedRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import javax.inject.Inject

@HiltViewModel
class FeedViewModel @Inject constructor(
    private val repository: FeedRepository,
) : ViewModel() {

    /**
     * `cachedIn(viewModelScope)` is not optional here.
     *
     * Without it the paging flow is re-collected on every configuration change
     * and every recomposition that resubscribes, which refetches page one and
     * throws away the user's scroll position. With it the loaded pages survive
     * rotation.
     */
    val items: Flow<PagingData<FeedItem>> =
        repository.feed(FeedSurface.Home).cachedIn(viewModelScope)

    private val _pendingActions = MutableStateFlow(FeedActionState())
    val pendingActions: StateFlow<FeedActionState> = _pendingActions.asStateFlow()

    /**
     * Interactions are recorded locally, not written through.
     *
     * The feed's own item carries `is_bookmarked` and counts, but a PagingData
     * stream is immutable — an item cannot be edited in place without either a
     * Room-backed mediator or a full refresh that loses scroll position. So a
     * tap here records intent in [FeedActionState], the card reflects it
     * immediately, and the write lands with the post slice.
     *
     * This is a deliberate seam, not a stub: wiring writes into the list before
     * the mediator exists would mean a refresh silently reverting what the user
     * just did, which is worse than an action that visibly does nothing yet.
     */
    fun onLocalBookmark(postId: String) = _pendingActions.update {
        it.copy(bookmarked = it.bookmarked.toggle(postId))
    }

    fun onLocalReaction(postId: String) = _pendingActions.update {
        it.copy(reacted = it.reacted.toggle(postId))
    }

    private fun Set<String>.toggle(id: String): Set<String> =
        if (contains(id)) this - id else this + id
}

/**
 * Local, session-scoped interaction state layered over the immutable page.
 *
 * Sets rather than a map of booleans: absence means "as the server reported
 * it", which is different from an explicit false.
 */
data class FeedActionState(
    val bookmarked: Set<String> = emptySet(),
    val reacted: Set<String> = emptySet(),
)
