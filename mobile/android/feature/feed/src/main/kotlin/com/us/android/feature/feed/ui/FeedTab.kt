package com.us.android.feature.feed.ui

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The three tabs under the home header, in the Figma frame's order.
 *
 * Two are the same timeline narrowed server-side ([FeedTab.FOLLOWING] is
 * `following_only=true`); the third is not a timeline at all but the day's
 * trending tags, each opening its own post list. The label is the user-visible
 * string exactly as the frame spells it.
 */
enum class FeedTab(val label: String) {
    FOR_YOU("For You"),
    FOLLOWING("Following"),
    HASHTAG("HashTag"),
}

/**
 * Which tab the home feed is on, for the life of the process.
 *
 * Held outside the ViewModel on purpose: the Home destination's state is
 * saved and restored across tab switches, but a process-scoped holder is the
 * simplest guarantee that leaving for Reels and coming back — or opening a
 * post and pressing Back — lands on the tab the reader chose rather than
 * quietly resetting to For You. It resets with the process, which is the
 * "per session" the product asks for.
 */
@Singleton
class FeedTabState @Inject constructor() {
    private val _selected = MutableStateFlow(FeedTab.FOR_YOU)
    val selected: StateFlow<FeedTab> = _selected.asStateFlow()

    fun select(tab: FeedTab) {
        _selected.value = tab
    }
}
