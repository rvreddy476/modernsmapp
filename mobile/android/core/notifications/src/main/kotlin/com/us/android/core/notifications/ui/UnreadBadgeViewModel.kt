package com.us.android.core.notifications.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.notifications.data.UnreadBadge
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Exposes the shared unread count to the Momentum header — Slice D. Lives in
 * `:core:notifications` because the header is worn by Home, Reels, Friends
 * AND Me, and two feature modules cannot share a ViewModel any other way.
 *
 * Deliberately tiny, and deliberately NOT part of `FeedViewModel`: the badge
 * has nothing to do with the timeline, and folding it in would make every
 * unread-count change recompose feed state.
 *
 * The count itself lives in [UnreadBadge], a singleton written by the inbox.
 * This only observes it and asks for a refresh when the feed appears — so
 * returning to the feed after clearing the inbox shows the cleared badge
 * immediately, and a notification that arrived while away is picked up on the
 * next look.
 */
@HiltViewModel
class UnreadBadgeViewModel @Inject constructor(
    private val badge: UnreadBadge,
) : ViewModel() {

    val count: StateFlow<Int> = badge.count

    fun refresh() {
        viewModelScope.launch { badge.refresh() }
    }
}
