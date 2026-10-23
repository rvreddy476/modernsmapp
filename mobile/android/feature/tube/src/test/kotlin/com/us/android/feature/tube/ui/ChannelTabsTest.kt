package com.us.android.feature.tube.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.tube.ui.channel.ChannelTab
import com.us.android.feature.tube.ui.channel.channelTabsFor
import org.junit.Test

/** The channel's tabs: Videos always, Members while a level is offered (2026-10-23), Appears in when it holds something. */
class ChannelTabsTest {

    @Test
    fun `each tab is offered only with something behind it, in order`() {
        assertThat(channelTabsFor(members = false, appearsIn = false)).containsExactly(ChannelTab.VIDEOS)
        assertThat(channelTabsFor(members = true, appearsIn = false))
            .containsExactly(ChannelTab.VIDEOS, ChannelTab.MEMBERS).inOrder()
        assertThat(channelTabsFor(members = true, appearsIn = true))
            .containsExactly(ChannelTab.VIDEOS, ChannelTab.MEMBERS, ChannelTab.APPEARS_IN).inOrder()
        assertThat(channelTabsFor(members = false, appearsIn = true))
            .containsExactly(ChannelTab.VIDEOS, ChannelTab.APPEARS_IN).inOrder()
    }
}
