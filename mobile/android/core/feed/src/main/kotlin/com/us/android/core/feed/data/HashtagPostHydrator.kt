package com.us.android.core.feed.data

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
 * The home timeline needs almost nothing — feed-service hydrates it
 * server-side — so the interface exists for the surfaces that DON'T come
 * through feed-service and arrive as bare post-service rows, and for the
 * one thing feed-service leaves out on every surface: the chosen cover
 * (see [coverIdToResolve]). Tests substitute the identity.
 */
fun interface FeedItemHydrator {
    suspend fun hydrate(items: List<FeedItem>): List<FeedItem>
}

/**
 * Resolves the author, the media delivery and the chosen cover for rows
 * that lack them.
 *
 * `GET /v1/hashtags/{tag}/posts` returns `PostDetail`: the same engagement and
 * counts fields as a feed item, but with only `author_id` (no `author`
 * object) and media as `{media_id, kind, position, alt_*}` references with no
 * dimensions and no signed URLs. Rendered as-is, every row would read
 * "Unnamed" over an empty image box. This is the per-page batch that stops
 * that — the same two lookups post detail makes for one post, made once per
 * distinct id per page rather than once per row.
 *
 * ## IDEMPOTENT ON PURPOSE (cover fix, 2026-09-05)
 *
 * A feed-service row already carries its author and its media delivery;
 * asking for either again would be an N+1 the server already paid for. So
 * every lookup here is keyed on what is MISSING — an author with no name, a
 * media reference with no delivery — which lets the same hydrator run over
 * every surface. What a feed row is always missing is its cover: the reel
 * and video forms upload the chosen frame as a separate image and the post
 * records it as `cover_media_id`, but the post's `media` list holds only
 * the video, and feed-service hydrates only that list. Without this pass the
 * cover the user picked never reaches a card; with it the cover arrives as
 * `FeedItem.coverMedia` and `videoThumb` finds it.
 *
 * Every lookup is best-effort. A profile that fails leaves the row with the
 * server's id (the card falls back to "Unnamed", as the feed does for a
 * missing author object); a delivery that fails leaves the reference bare and
 * the card shows the text without the image; a cover that fails leaves the
 * video's own still. A page never fails because one asset did.
 */
class HashtagPostHydrator @Inject constructor(
    private val profiles: ProfileRepository,
    private val media: MediaApi,
    private val cache: MediaDeliveryCache,
) : FeedItemHydrator {

    override suspend fun hydrate(items: List<FeedItem>): List<FeedItem> = coroutineScope {
        val authorIds = items.filter { it.author.needsLookup }.map { it.authorId }.filter { it.isNotBlank() }.distinct()
        val mediaIds = items.flatMap { it.media }.filter { it.needsDelivery }.map { it.mediaId }
            .filter { it.isNotBlank() }
        val coverIds = items.mapNotNull { it.coverIdToResolve() }
        val wantedDeliveries = (mediaIds + coverIds).distinct()

        // Both batches start together; each id is fetched at most once.
        val authorJobs = authorIds.map { id -> async { id to loadAuthor(id) } }
        val mediaJobs = wantedDeliveries.map { id -> async { id to loadDelivery(id) } }
        val authors = authorJobs.awaitAll().toMap()
        val deliveries = mediaJobs.awaitAll().toMap()

        items.map { item ->
            val cover = item.coverIdToResolve()?.let { id -> deliveries[id]?.let { coverMedia(id, it) } }
            item.copy(
                author = authors[item.authorId]?.toFeedAuthor() ?: item.author,
                media = item.media.map { ref ->
                    if (ref.needsDelivery) deliveries[ref.mediaId]?.let { ref.withDelivery(it) } ?: ref else ref
                },
                coverMedia = cover ?: item.coverMedia,
            )
        }
    }

    private suspend fun loadAuthor(id: String): Profile? =
        (profiles.getProfile(id) as? AppResult.Success)?.data

    /** The cached delivery while its URLs are fresh; else the endpoint's answer, cached when it can be drawn. */
    @Suppress("TooGenericExceptionCaught")
    private suspend fun loadDelivery(id: String): MediaDeliveryDto? = cache.get(id) ?: try {
        media.getDelivery(id).data?.also { cache.put(id, it) }
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        // Retrofit/okio/serialization failures alike: the row renders without
        // its image rather than the page failing. The error is not mapped
        // because nothing can act on it here.
        null
    }
}

/**
 * The cover id a row names but does not carry as media — the one to fetch.
 * Null when the row has no cover, already lists it among its media (a server
 * that starts embedding covers needs no client fetch), or already resolved it.
 */
fun FeedItem.coverIdToResolve(): String? =
    controls.coverMediaId?.takeIf { id ->
        id.isNotBlank() && media.none { it.mediaId == id } && coverMedia?.mediaId != id
    }

/**
 * A reference with nothing a card can draw: no variant (no `thumb_150`, no
 * cover rung) and no wash.
 *
 * Post-service's `PostDetail` overlays `hls_url`, `processing_status` and
 * `duration_ms` on its media but never `variants` or `blurhash` — a video
 * from `GET /v1/posts/{id}`, by-author, bookmarks or continue-watching can
 * PLAY without a second call but cannot show a still. Treating a present
 * `hls_url` as "delivered" was the continue-watching blank-card bug
 * (2026-09-05): the shelf's rows were the only Tube rows that never got
 * their delivery. Feed-service rows always carry variants, so they are
 * still never re-fetched.
 */
private val FeedMedia.needsDelivery: Boolean
    get() = variants.isEmpty() && blurhash.isBlank()

/** A bare `author_id` row: post-service sends no author object, so the name is blank. */
private val FeedAuthor.needsLookup: Boolean
    get() = displayName.isBlank() && username.isNullOrBlank()

private fun Profile.toFeedAuthor() = FeedAuthor(
    id = userId,
    displayName = displayName,
    username = username.takeIf { it.isNotBlank() },
    avatarMediaId = avatarMediaId,
)

/** The cover as a media entry of its own, so every still-picker resolves it the way it resolves a row's media. */
private fun coverMedia(id: String, delivery: MediaDeliveryDto) = FeedMedia(
    mediaId = id,
    kind = delivery.kind.ifBlank { COVER_KIND },
    altDecorative = true,
).withDelivery(delivery)

private const val COVER_KIND = "image"

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
    // A post-service row may already name its playlist; a delivery that
    // does not (the transcode's state read a moment earlier) must not erase it.
    hlsUrl = delivery.hlsUrl ?: hlsUrl,
    expiresAt = delivery.expiresAt,
    processingStatus = delivery.processingStatus.ifBlank { processingStatus },
    moderationStatus = delivery.moderationStatus.ifBlank { moderationStatus },
    playbackUrl = delivery.playbackUrl ?: playbackUrl,
    playbackKind = delivery.playbackKind.ifBlank { playbackKind },
    // The delivery's measurement wins; the bare reference's stays when it has none.
    durationMs = delivery.durationMs.takeIf { it > 0L } ?: durationMs,
)
