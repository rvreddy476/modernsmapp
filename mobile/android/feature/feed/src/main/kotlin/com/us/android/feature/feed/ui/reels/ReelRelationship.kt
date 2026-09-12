package com.us.android.feature.feed.ui.reels

import com.us.android.core.feed.data.offersFollow
import com.us.android.core.feed.data.offersSubscribe
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus

/**
 * The one relationship pill a reel's overlay can draw: Subscribe toward
 * the author's Tube channel, or Follow toward the author.
 *
 * Subscribe only when the row CARRIES a channel. A subscribe is
 * `POST v1/channels/{ref}/subscribe`, and for an author with no channel
 * that answers 404, so a reel that shows Subscribe on the strength of the
 * author alone would offer a tap that cannot succeed. Follow is the edge
 * every author has. Today feed-service attaches `channel` to `long_video`
 * rows only, so every reel resolves to Follow until the server embeds the
 * author's channel on short rows too; the rule is written against the row
 * rather than the content type so that day needs no client change.
 */
sealed interface ReelRelationship {
    /** What the tap is keyed by: the channel's user id, or the author's. */
    val ref: String
    val label: String

    data class Follow(override val ref: String) : ReelRelationship {
        override val label: String get() = "Follow"
    }

    data class Subscribe(override val ref: String) : ReelRelationship {
        override val label: String get() = "Subscribe"
    }
}

/** Subscribe when the row carries a channel, Follow otherwise. */
fun reelRelationship(item: FeedItem): ReelRelationship {
    val channelId = item.channel?.userId?.takeIf { it.isNotBlank() }
    return if (channelId != null) ReelRelationship.Subscribe(channelId) else ReelRelationship.Follow(item.author.id)
}

/**
 * Whether the pill is drawn: each edge is read from its own graph, under
 * that graph's rule (known, not the viewer's own, not already made).
 */
fun offersReelRelationship(
    relationship: ReelRelationship,
    ownId: String,
    followEdges: Map<String, FollowStatus>,
    subscriptionEdges: Map<String, ChannelSubscription>,
): Boolean = when (relationship) {
    is ReelRelationship.Follow -> offersFollow(ownId, relationship.ref, followEdges[relationship.ref])
    is ReelRelationship.Subscribe -> offersSubscribe(ownId, relationship.ref, subscriptionEdges[relationship.ref])
}
