package com.us.android.feature.tube.ui.watch

import com.us.android.core.model.FeedItem

/**
 * The channel the watch screen's Subscribe button points at: the channel
 * embedded on the row when the feed carries one, the author otherwise.
 *
 * The two are the same person (one channel per user, keyed by the user
 * id) and `v1/channels/{ref}` accepts either, so the fallback is not a
 * guess: a long video from a server that predates the embed still has a
 * channel, because posting one without a channel is refused. Keying on
 * the channel first keeps the button and the row's name and `@handle`
 * pointing at the same identity.
 */
fun subscribeRef(item: FeedItem): String = item.channel?.userId?.takeIf { it.isNotBlank() } ?: item.author.id
