package com.us.android.feature.tube.data

import com.us.android.core.model.FeedItem
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The videos the watch screen plays through: the page Tube home had loaded
 * when a card was tapped, in its order.
 *
 * "Up next" is the rest of THAT list, and the auto-advance at the end of a
 * video is its next row — so the watch screen needs the list, and a route
 * argument is the wrong place for a page of hydrated posts. Tube home writes
 * it as the viewer leaves for a video; the watch screen reads it. The same
 * holder shape as `ReelsEntry`, scoped to this feature.
 */
@Singleton
class TubeQueue @Inject constructor() {

    private val _items = MutableStateFlow<List<FeedItem>>(emptyList())

    /** The list the watch screen plays through; empty when Tube home has not been visited. */
    val items: StateFlow<List<FeedItem>> = _items.asStateFlow()

    fun set(items: List<FeedItem>) {
        _items.value = items
    }
}
