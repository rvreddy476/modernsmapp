package com.us.android.feature.tube.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.ChannelState
import com.us.android.core.model.Channel
import org.junit.Test

/** The header's More sheet: which rows it shows for what is known about the viewer's channel. */
class TubeMenuTest {

    private val channel = Channel(userId = "u1", name = "Clee", handle = "clee")

    @Test
    fun `a channel leads with Your channel, then the four fixed rows`() {
        assertThat(tubeMenuRows(ChannelState.Present(channel)))
            .containsExactly(
                TubeMenuRow.YOUR_CHANNEL,
                TubeMenuRow.SUBSCRIPTIONS,
                TubeMenuRow.SCHEDULED,
                TubeMenuRow.SAVED,
                TubeMenuRow.NOTIFICATIONS,
            )
            .inOrder()
    }

    @Test
    fun `no channel on the server offers to create one`() {
        assertThat(tubeMenuRows(ChannelState.None).first()).isEqualTo(TubeMenuRow.CREATE_CHANNEL)
        assertThat(tubeMenuRows(ChannelState.None)).doesNotContain(TubeMenuRow.YOUR_CHANNEL)
    }

    @Test
    fun `an unknown or failed lookup does not invent the channel's absence`() {
        assertThat(tubeMenuRows(ChannelState.Unknown).first()).isEqualTo(TubeMenuRow.YOUR_CHANNEL)
        assertThat(tubeMenuRows(ChannelState.Failed("offline")).first()).isEqualTo(TubeMenuRow.YOUR_CHANNEL)
    }

    @Test
    fun `every row has a label and none is empty`() {
        TubeMenuRow.entries.forEach { assertThat(it.label).isNotEmpty() }
        assertThat(tubeMenuRows(ChannelState.Present(channel))).hasSize(5)
    }
}
