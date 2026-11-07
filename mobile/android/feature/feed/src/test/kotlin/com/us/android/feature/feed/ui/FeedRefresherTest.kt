package com.us.android.feature.feed.ui

import androidx.paging.LoadState
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.feed.data.AppErrorException
import org.junit.Test

/**
 * A refresh the reader asked for (audit 2026-10-30, A5). A failed one with
 * posts on screen used to end in silence — the spinner went away and the
 * stale feed stayed — so offline looked like up to date. Now it says why,
 * in the stage's message host, and the posts stay.
 */
class FeedRefresherTest {

    private var reloads = 0
    private val refresher = FeedRefresher(reload = { reloads++ }, now = { NOW })

    private val offline = LoadState.Error(AppErrorException(AppError.NoNetwork()))

    @Test
    fun `a refresh asks the list again and is in flight`() {
        refresher.refresh()

        assertThat(reloads).isEqualTo(1)
        assertThat(refresher.inFlight).isTrue()
    }

    @Test
    fun `a failed refresh over posts on screen says why`() {
        refresher.refresh()

        val landed = refresher.onRefreshState(offline, postsOnScreen = true)

        assertThat(landed).isFalse()
        assertThat(refresher.inFlight).isFalse()
        assertThat(refresher.failure?.text).isEqualTo("You're offline. Check your connection and try again.")
        assertThat(refresher.failure?.id).isEqualTo(NOW)
    }

    /** With nothing on screen the full error state says it; a message on top would say it twice. */
    @Test
    fun `a failed refresh over an empty feed raises no message`() {
        refresher.refresh()

        refresher.onRefreshState(offline, postsOnScreen = false)

        assertThat(refresher.inFlight).isFalse()
        assertThat(refresher.failure).isNull()
    }

    @Test
    fun `a refresh that lands goes to the first post and says nothing`() {
        refresher.refresh()

        assertThat(refresher.onRefreshState(LoadState.Loading, postsOnScreen = true)).isFalse()
        assertThat(refresher.inFlight).isTrue()
        assertThat(refresher.onRefreshState(LoadState.NotLoading(endOfPaginationReached = false), true)).isTrue()
        assertThat(refresher.inFlight).isFalse()
        assertThat(refresher.failure).isNull()
    }

    /** The list's own first load, or a retry from the error state, is not the reader's refresh. */
    @Test
    fun `an error nobody asked a refresh for says nothing`() {
        assertThat(refresher.onRefreshState(offline, postsOnScreen = true)).isFalse()
        assertThat(refresher.failure).isNull()
    }

    @Test
    fun `the next refresh clears the old message, and so does dismissing it`() {
        refresher.refresh()
        refresher.onRefreshState(offline, postsOnScreen = true)

        refresher.refresh()
        assertThat(refresher.failure).isNull()

        refresher.onRefreshState(offline, postsOnScreen = true)
        refresher.dismissFailure()
        assertThat(refresher.failure).isNull()
    }

    private companion object {
        const val NOW = 42L
    }
}
