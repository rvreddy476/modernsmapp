package com.us.android.feature.feed.ui

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The two tabs on the home header: the same timeline, [FeedTab.FOLLOWING]
 * narrowed server-side (`following_only=true`). The HashTag tab was removed
 * (founder, 2026-10-19); a tapped #tag still opens its own post list.
 */
enum class FeedTab(val label: String) {
    FOR_YOU("For You"),
    FOLLOWING("Following"),
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
