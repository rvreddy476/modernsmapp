package com.us.android.feature.tube.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.ChannelState
import com.us.android.core.model.Channel
import org.junit.Test

/**
 * The header's More sheet: which rows it shows for what is known about the
 * viewer's channel, and the order it shows them in.
 *
 * 2026-10-02: the order changed deliberately. It was the channel row then
 * four fixed rows; it is now ascending alphabetical by label (the founder's
 * rule for every menu), with Collections and Watch later added.
 */
class TubeMenuTest {

    private val channel = Channel(userId = "u1", name = "Clee", handle = "clee")

    @Test
    fun `with a channel the rows read as one alphabetical list`() {
        assertThat(tubeMenuRows(ChannelState.Present(channel)).map { it.label })
            .containsExactly(
                "Collections",
                "Notifications",
                "Offline",
                "Saved",
                "Scheduled posts",
                "Subscriptions",
                "Watch later",
                "Your channel",
            )
            .inOrder()
    }

    @Test
    fun `every state of the channel is in ascending alphabetical order`() {
        val states = listOf(
            ChannelState.Present(channel),
            ChannelState.None,
            ChannelState.Unknown,
            ChannelState.Failed("offline"),
        )
        for (state in states) {
            val labels = tubeMenuRows(state).map { it.label.lowercase() }
            assertThat(labels).isInStrictOrder()
        }
    }

    @Test
    fun `no channel on the server offers to create one`() {
        assertThat(tubeMenuRows(ChannelState.None)).contains(TubeMenuRow.CREATE_CHANNEL)
        assertThat(tubeMenuRows(ChannelState.None)).doesNotContain(TubeMenuRow.YOUR_CHANNEL)
    }

    @Test
    fun `an unknown or failed lookup does not invent the channel's absence`() {
        assertThat(tubeMenuRows(ChannelState.Unknown)).contains(TubeMenuRow.YOUR_CHANNEL)
        assertThat(tubeMenuRows(ChannelState.Unknown)).doesNotContain(TubeMenuRow.CREATE_CHANNEL)
        assertThat(tubeMenuRows(ChannelState.Failed("offline"))).contains(TubeMenuRow.YOUR_CHANNEL)
    }

    /** The two lists the watch page fills are reachable from the menu, or a saved video could not be found again. */
    @Test
    fun `watch later and collections are always offered`() {
        for (state in listOf(ChannelState.Present(channel), ChannelState.None, ChannelState.Unknown)) {
            assertThat(tubeMenuRows(state)).containsAtLeast(
                TubeMenuRow.WATCH_LATER,
                TubeMenuRow.COLLECTIONS,
                TubeMenuRow.SAVED,
            )
        }
    }

    /**
     * 2026-10-02: Offline joined the menu (the count below went from 7 to 8,
     * deliberately). It is offered whatever is known of the channel, and a
     * failed channel lookup is exactly the state a phone with no network is
     * in: the page of what the device keeps must be reachable then.
     */
    @Test
    fun `offline is always offered, with or without a network's answer about the channel`() {
        val states = listOf(
            ChannelState.Present(channel),
            ChannelState.None,
            ChannelState.Unknown,
            ChannelState.Failed("offline"),
        )
        for (state in states) {
            assertThat(tubeMenuRows(state)).contains(TubeMenuRow.OFFLINE)
        }
        assertThat(TubeMenuRow.OFFLINE.label).isEqualTo("Offline")
    }

    @Test
    fun `every row has a label and none is empty`() {
        TubeMenuRow.entries.forEach { assertThat(it.label).isNotEmpty() }
        assertThat(tubeMenuRows(ChannelState.Present(channel))).hasSize(8)
    }
}
