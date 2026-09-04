package com.us.android.feature.feed.data

import com.us.android.core.model.CarouselOrdinals
import com.us.android.core.model.FeedMedia
import com.us.android.feature.feed.data.dto.FeedMediaDto

/**
 * Turns wire media into an ordered, validated carousel — Creator Studio P0-A.
 *
 * The feed is the surface where this matters most: it is where almost every
 * image is actually seen, and it is the one whose payload passes through batch
 * hydration and a response cache before arriving here. Both of those can
 * reorder a slice without producing an error anywhere.
 *
 * A rejected slice drops the media and keeps the post, for the same reason as
 * the detail screen: guessing an order publishes something the author did not
 * write, and throwing would take down the whole feed page for one bad row.
 */
internal fun List<FeedMediaDto>.toOrderedFeedMedia(): List<FeedMedia> {
    val items = map {
        FeedMedia(
            mediaId = it.mediaId,
            kind = it.kind,
            position = it.position,
            altText = it.altText,
            altDecorative = it.altDecorative,
            status = it.status,
            width = it.width,
            height = it.height,
            blurhash = it.blurhash,
            variants = it.variants,
            hlsUrl = it.hlsUrl,
            expiresAt = it.expiresAt,
            processingStatus = it.processingStatus,
            moderationStatus = it.moderationStatus,
            playbackUrl = it.playbackUrl,
            playbackKind = it.playbackKind,
        )
    }
    return when (val ordered = CarouselOrdinals.order(items) { it.position }) {
        is CarouselOrdinals.Result.Ordered -> ordered.items
        is CarouselOrdinals.Result.Rejected -> emptyList()
    }
}
