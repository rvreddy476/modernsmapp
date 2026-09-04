package com.us.android.feature.feed.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.media.publish.ReelPublishActions
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The feed's view of a background reel publish — see [ReelPublishBanner].
 *
 * Reads the shared [ReelPublishTracker] and forwards Retry, Discard and
 * View's dismiss to [ReelPublishActions], which `:feature:post` implements
 * behind the core port; this module never learns what the worker is.
 *
 * "Your reel is live" dismisses itself after [AUTO_DISMISS_MILLIS]: it is
 * good news, not a decision, and a banner that waits to be closed is the
 * kind of thing that stays up all day. A failure waits — it IS a decision.
 */
@HiltViewModel
class ReelPublishBannerViewModel @Inject constructor(
    tracker: ReelPublishTracker,
    private val actions: ReelPublishActions,
) : ViewModel() {

    val state: StateFlow<ReelPublishState> = tracker.state

    init {
        viewModelScope.launch {
            // collectLatest: a state change during the wait cancels the
            // pending dismiss, so a new publish that starts within six
            // seconds is never dismissed by the old one's timer.
            state.collectLatest { current ->
                if (current is ReelPublishState.Published) {
                    delay(AUTO_DISMISS_MILLIS)
                    actions.dismiss()
                }
            }
        }
    }

    fun retry() = actions.retry()

    fun discard() = actions.discard()

    fun dismiss() = actions.dismiss()

    companion object {
        const val AUTO_DISMISS_MILLIS = 6_000L
    }
}
