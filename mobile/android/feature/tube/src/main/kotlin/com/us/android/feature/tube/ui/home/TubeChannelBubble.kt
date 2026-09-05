package com.us.android.feature.tube.ui.home

import com.us.android.core.model.FeedItem

/**
 * One bubble on the channels strip: a creator the viewer follows who has
 * posted a long video — which, since channel-before-video (2026-09-05),
 * means a creator with a channel. [name] is the channel's name when the
 * row carries one and the author's otherwise (a server that predates the
 * embed), so the strip degrades to names rather than to nothing.
 */
data class TubeChannelBubble(
    val userId: String,
    val name: String,
    val handle: String?,
    val avatarUrl: String?,
    val avatarMediaId: String?,
)

/**
 * The strip from the Following feed: one bubble per author, in the order
 * their newest video appears — the feed is newest first, so the first row
 * an author has IS their newest. [ownId] is left out: the viewer's own
 * bubble is drawn first by the strip itself, from their channel.
 */
fun channelBubbles(items: List<FeedItem>, ownId: String): List<TubeChannelBubble> =
    items.asSequence()
        .filter { it.author.id.isNotBlank() && it.author.id != ownId }
        .distinctBy { it.author.id }
        .map { item ->
            TubeChannelBubble(
                userId = item.author.id,
                name = item.creatorName,
                handle = item.creatorHandle,
                avatarUrl = item.channel?.avatarUrl,
                avatarMediaId = item.author.avatarMediaId,
            )
        }
        .toList()
