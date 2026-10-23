package com.us.android.feature.post.data

import com.us.android.core.feed.data.copyright.CopyrightReviewRepository
import com.us.android.core.model.CopyrightReview
import javax.inject.Inject

/**
 * After a long video is published, where it stands in copyright title review
 * (2026-10-24). The create's answer does not carry `copyright_review`
 * (contract: "re-read the detail after publishing a long video"), so the
 * author's own `GET /v1/posts/:id` is read once and its review handed to the
 * publish tile. A port, so the publish pipeline is tested without the network.
 */
fun interface PublishedVideoReview {
    /** The review on [postId], or null when it has none or the read failed. */
    suspend fun reviewOf(postId: String): CopyrightReview?
}

class ApiPublishedVideoReview @Inject constructor(
    private val repository: CopyrightReviewRepository,
) : PublishedVideoReview {
    override suspend fun reviewOf(postId: String): CopyrightReview? = repository.reviewOf(postId)
}
