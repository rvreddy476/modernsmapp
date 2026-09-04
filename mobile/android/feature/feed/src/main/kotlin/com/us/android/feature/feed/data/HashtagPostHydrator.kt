package com.us.android.feature.feed.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.media.data.MediaApi
import com.us.android.core.media.data.MediaDeliveryDto
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.Profile
import com.us.android.core.profile.data.ProfileRepository
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import javax.inject.Inject

/**
 * Fills in whatever a page's rows are missing before they reach the card.
 *
 * The home timeline needs nothing — feed-service hydrates it server-side —
 * so the interface exists for the surfaces that DON'T come through
 * feed-service and arrive as bare post-service rows. Tests substitute the
 * identity.
 */
fun interface FeedItemHydrator {
    suspend fun hydrate(items: List<FeedItem>): List<FeedItem>
}

/**
 * Resolves the author and the media delivery for post-service rows.
 *
 * `GET /v1/hashtags/{tag}/posts` returns `PostDetail`: the same engagement and
 * counts fields as a feed item, but with only `author_id` (no `author`
 * object) and media as `{media_id, kind, position, alt_*}` references with no
 * dimensions and no signed URLs. Rendered as-is, every row would read
 * "Unnamed" over an empty image box. This is the per-page batch that stops
 * that — the same two lookups post detail makes for one post, made once per
 * distinct id per page rather than once per row.
 *
 * Every lookup is best-effort. A profile that fails leaves the row with the
 * server's id (the card falls back to "Unnamed", as the feed does for a
 * missing author object); a delivery that fails leaves the reference bare and
 * the card shows the text without the image. A page never fails because one
 * asset did.
 */
class HashtagPostHydrator @Inject constructor(
    private val profiles: ProfileRepository,
    private val media: MediaApi,
) : FeedItemHydrator {

    override suspend fun hydrate(items: List<FeedItem>): List<FeedItem> = coroutineScope {
        val authorIds = items.map { it.authorId }.filter { it.isNotBlank() }.distinct()
        val mediaIds = items.flatMap { it.media }.map { it.mediaId }.filter { it.isNotBlank() }.distinct()

        // Both batches start together; each id is fetched at most once.
        val authorJobs = authorIds.map { id -> async { id to loadAuthor(id) } }
        val mediaJobs = mediaIds.map { id -> async { id to loadDelivery(id) } }
        val authors = authorJobs.awaitAll().toMap()
        val deliveries = mediaJobs.awaitAll().toMap()

        items.map { item ->
            item.copy(
                author = authors[item.authorId]?.toFeedAuthor() ?: item.author,
                media = item.media.map { ref -> deliveries[ref.mediaId]?.let { ref.withDelivery(it) } ?: ref },
            )
        }
    }

    private suspend fun loadAuthor(id: String): Profile? =
        (profiles.getProfile(id) as? AppResult.Success)?.data

    @Suppress("TooGenericExceptionCaught")
    private suspend fun loadDelivery(id: String): MediaDeliveryDto? = try {
        media.getDelivery(id).data
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        // Retrofit/okio/serialization failures alike: the row renders without
        // its image rather than the page failing. The error is not mapped
        // because nothing can act on it here.
        null
    }
}

private fun Profile.toFeedAuthor() = FeedAuthor(
    id = userId,
    displayName = displayName,
    username = username.takeIf { it.isNotBlank() },
    avatarMediaId = avatarMediaId,
)

/**
 * The reference plus its delivery — the shape feed-service hands the card.
 * The reference's own accessibility decision and ordinal are kept; only what
 * the delivery endpoint knows is taken from it.
 */
private fun FeedMedia.withDelivery(delivery: MediaDeliveryDto) = copy(
    kind = delivery.kind.ifBlank { kind },
    status = delivery.status,
    width = delivery.width,
    height = delivery.height,
    blurhash = delivery.blurhash,
    variants = delivery.variants,
    hlsUrl = delivery.hlsUrl,
    expiresAt = delivery.expiresAt,
    processingStatus = delivery.processingStatus,
    moderationStatus = delivery.moderationStatus,
    playbackUrl = delivery.playbackUrl,
    playbackKind = delivery.playbackKind,
)
