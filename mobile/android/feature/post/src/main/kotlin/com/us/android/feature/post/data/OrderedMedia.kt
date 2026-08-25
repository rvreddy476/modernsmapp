package com.us.android.feature.post.data

import com.us.android.core.model.CarouselOrdinals
import com.us.android.core.model.PostMediaRef
import com.us.android.feature.post.data.dto.PostMediaDto

/**
 * Turns wire media into an ordered, validated carousel — Creator Studio P0-A.
 *
 * ## WHY A REJECTED SLICE BECOMES AN EMPTY ONE
 *
 * When the ordinals do not agree with each other, this drops the media rather
 * than guessing an order. The post still renders — its text, its author, its
 * counts — but without images.
 *
 * That is the least-bad outcome of three. Rendering the pages in an arbitrary
 * order silently publishes a different post than the author wrote. Throwing
 * would take down the whole feed page or the whole detail screen for one bad
 * post. Dropping the media is visible, contained, and honest.
 */
internal fun List<PostMediaDto>.toOrderedRefs(): List<PostMediaRef> {
    val refs = map {
        PostMediaRef(
            mediaId = it.mediaId,
            kind = it.kind,
            position = it.position,
            altText = it.altText,
            altDecorative = it.altDecorative,
        )
    }
    return when (val ordered = CarouselOrdinals.order(refs) { it.position }) {
        is CarouselOrdinals.Result.Ordered -> ordered.items
        is CarouselOrdinals.Result.Rejected -> emptyList()
    }
}
