package com.us.android.feature.tube.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.Channel
import com.us.android.feature.tube.ui.channel.ChannelTab
import com.us.android.feature.tube.ui.channel.ChannelTabCounts
import com.us.android.feature.tube.ui.channel.channelEmptyTitle
import com.us.android.feature.tube.ui.channel.channelTabsFor
import com.us.android.feature.tube.ui.channel.tabCounts
import org.junit.Test

/**
 * The channel's tabs (web parity, 2026-10-24: the web's channelModel.ts
 * `visibleTabs`): Videos, Shorts, Live, Collections, Posts, then Members
 * while a level is offered (2026-10-23) and Appears in when it holds something.
 */
class ChannelTabsTest {

    private val base = listOf(ChannelTab.VIDEOS, ChannelTab.SHORTS, ChannelTab.LIVE, ChannelTab.COLLECTIONS, ChannelTab.POSTS)

    @Test
    fun `with nothing known about the counts every tab stays, in the web's order`() {
        assertThat(channelTabsFor(members = false, appearsIn = false)).containsExactlyElementsIn(base).inOrder()
        assertThat(channelTabsFor(members = true, appearsIn = true))
            .containsExactlyElementsIn(base + listOf(ChannelTab.MEMBERS, ChannelTab.APPEARS_IN)).inOrder()
        assertThat(channelTabsFor(members = false, appearsIn = true))
            .containsExactlyElementsIn(base + ChannelTab.APPEARS_IN).inOrder()
    }

    @Test
    fun `a visitor does not see a tab the channel says is empty, Videos and Posts aside`() {
        val counts = ChannelTabCounts(shorts = 0, live = 0, collections = 0)
        assertThat(channelTabsFor(members = false, appearsIn = false, counts = counts))
            .containsExactly(ChannelTab.VIDEOS, ChannelTab.POSTS).inOrder()
        assertThat(channelTabsFor(members = false, appearsIn = false, counts = ChannelTabCounts(shorts = 3, live = 0, collections = null)))
            .containsExactly(ChannelTab.VIDEOS, ChannelTab.SHORTS, ChannelTab.COLLECTIONS, ChannelTab.POSTS).inOrder()
    }

    @Test
    fun `the owner sees every tab, empty or not`() {
        val counts = ChannelTabCounts(shorts = 0, live = 0, collections = 0)
        assertThat(channelTabsFor(members = false, appearsIn = false, counts = counts, isOwner = true))
            .containsExactlyElementsIn(base).inOrder()
    }

    @Test
    fun `while the channel is being read only Videos and the extras are offered`() {
        assertThat(channelTabsFor(members = false, appearsIn = false, counts = null)).containsExactly(ChannelTab.VIDEOS)
        assertThat(channelTabsFor(members = true, appearsIn = true, counts = null, isOwner = true))
            .containsExactly(ChannelTab.VIDEOS, ChannelTab.MEMBERS, ChannelTab.APPEARS_IN).inOrder()
    }

    @Test
    fun `the counts come off the channel read, and each empty tab says what it lacks`() {
        val channel = Channel(userId = "u", name = "K", handle = "k", shortCount = 2, liveCount = 0, collectionCount = null)
        assertThat(channel.tabCounts()).isEqualTo(ChannelTabCounts(shorts = 2, live = 0, collections = null))
        assertThat(ChannelTab.entries.map(::channelEmptyTitle)).containsNoDuplicates()
        assertThat(channelEmptyTitle(ChannelTab.LIVE)).isEqualTo("No past live streams yet")
    }
}
