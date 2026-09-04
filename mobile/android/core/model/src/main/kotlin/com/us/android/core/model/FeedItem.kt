package com.us.android.core.model

/**
 * One entry in a feed.
 *
 * Feed items are HYDRATED posts, not post-id rows, and as of the 2026-08-17
 * server-side hydration closure they are hydrated far enough to render a whole
 * row with no follow-up call: author identity, authorized media delivery, and
 * per-viewer action state all arrive inline.
 *
 * That is the difference between one request per screen and one request per
 * row. The client deliberately does NOT resolve any of it per item — the
 * earlier design, where the card fell back to a truncated user id and a
 * placeholder for media, existed only because the server did not yet send
 * these fields.
 *
 * It is still NOT [Post]. The two overlap but differ: a feed item carries
 * `feed_content_type`, `app_origin` and an optional ranking `score`, and lacks
 * the distribution metadata post detail returns. Modelling them as one type
 * would force every absent field to be nullable.
 */
data class FeedItem(
    val id: String,
    val authorId: String,
    val author: FeedAuthor,
    val text: String,
    /**
     * The long video's title (Tube, 2026-09-05). Blank on every other kind of
     * post and on rows from a server that predates the field.
     */
    val title: String = "",
    val visibility: String,
    /** `post`, `flick`, `long_video`, … — the feed's own classification. */
    val feedContentType: String,
    /** `text`, `image`, `video`, … — how to render the body. */
    val postType: String,
    val createdAt: String,
    val isPinned: Boolean,
    val media: List<FeedMedia>,
    val counts: FeedCounts,
    val viewer: FeedViewerState,
    val isRepostable: Boolean,
    /**
     * Ranking score, present only on the ranked surfaces (reels, videos,
     * watch). Home is chronological and omits it. Kept because its presence is
     * itself the signal that a surface is ranked.
     */
    val score: Double? = null,
    /** Present exactly when this post is a poll. */
    val poll: FeedPoll? = null,
    /**
     * The server is still transcoding or moderating this post's video.
     *
     * Since 2026-09-04 a flick is created the moment its upload is confirmed
     * and is visible to its author at once; everyone else sees it when this
     * turns false. The playback URL meanwhile is the original file.
     */
    val isProcessing: Boolean = false,
    /** The author's per-post switches — what viewers may do with it. */
    val controls: FeedPostControls = FeedPostControls(),
    /**
     * "Why you're seeing this post" (the post "more" sheet, 2026-09-04).
     *
     * [reason] is the stable token feed-service derives at hydration —
     * `following`, `connection`, `trending`, `category:<id>`, `recommended` —
     * and [reasonText] the sentence it wants shown verbatim ("From someone
     * you follow", "Popular in Comedy"). Both are blank on the viewer's own
     * posts and on rows from a server that predates them; the sheet shows the
     * row only when there is a sentence to put in it.
     */
    val reason: String = "",
    val reasonText: String = "",
)

/**
 * The author-set controls the reel form sends and the feed hands back.
 *
 * Defaults are the open case: an older row that predates the fields renders
 * every control, which is what it did before the fields existed.
 */
data class FeedPostControls(
    /** Comments are off: hide the comment control. */
    val noComments: Boolean = false,
    /** The author hid the share control. */
    val hideShare: Boolean = false,
    val allowDownload: Boolean = true,
    /** `allow` or `disallow`. */
    val remixSetting: String = "",
    val category: String = "",
    val taggedUserIds: List<String> = emptyList(),
    val locationName: String = "",
    val coverMediaId: String? = null,
)

/**
 * A hydrated poll, delivered inline like everything else on a feed row.
 *
 * [viewerVotedOptionIds] is what flips the card from ballots to results: a
 * viewer who has voted (or a poll that has ended) sees counts, everyone else
 * sees choices. The percentages are server-computed; the client never derives
 * them from counts, so both stay authoritative.
 */
data class FeedPoll(
    val question: String,
    val allowsMultiple: Boolean,
    val options: List<FeedPollOption>,
    val totalVotes: Long,
    val viewerVotedOptionIds: List<String> = emptyList(),
    val hasEnded: Boolean = false,
) {
    val showResults: Boolean get() = hasEnded || viewerVotedOptionIds.isNotEmpty()
}

data class FeedPollOption(
    val id: String,
    val label: String,
    val voteCount: Long,
    val percentage: Double,
)

/**
 * Minimal author identity, embedded by the server's page-level profile batch.
 *
 * [displayName] can legitimately be blank — `PUT /v1/profiles/me` is a full
 * replacement that clears strings — and the server substitutes a
 * non-enumerating "Deleted account" for a profile that genuinely no longer
 * exists. Neither case should render an empty row, which is what
 * [nameForDisplay] handles.
 */
data class FeedAuthor(
    val id: String,
    val displayName: String,
    val username: String? = null,
    val avatarMediaId: String? = null,
) {
    val nameForDisplay: String get() = displayName.ifBlank { username ?: "Unnamed" }
}

/**
 * A media reference WITH its authorized delivery URLs.
 *
 * Two URL kinds live here and they behave nothing alike:
 *
 *  - [hlsUrl] is **gateway-relative** (`/v1/media/:id/hls/master.m3u8`) and the
 *    route is authorized, so it must be resolved against the API base URL and
 *    fetched with the bearer token.
 *  - [variants] values are **absolute, pre-signed object-store URLs** and must
 *    be used exactly as returned.
 *
 * Resolving them is `core/media`'s `MediaUrlResolver`, never string
 * concatenation here. Storage keys elsewhere in the payload must never be used
 * to build a URL — the contract capture warns against it explicitly.
 */
data class FeedMedia(
    val mediaId: String,
    /** `image` or `video` in the captured fixtures. */
    val kind: String,
    /**
     * The author's accessibility decision — Slice C, C-CLB-3.
     *
     * Delivered by feed hydration alongside the delivery fields. The feed is
     * where most images are seen, so a feed that drops these makes the
     * composer's mandatory description pointless for almost every reader.
     */
    val altText: String = "",
    val altDecorative: Boolean = false,
    /** Carousel ordinal; -1 when the payload predates the field. */
    val position: Int = CarouselOrdinals.ABSENT,
    val status: String = "",
    val width: Int = 0,
    val height: Int = 0,
    /** Compact placeholder gradient; render while the real bytes load. */
    val blurhash: String = "",
    val variants: Map<String, String> = emptyMap(),
    val hlsUrl: String? = null,
    /**
     * When the signed URLs above stop working — five minutes after issue, per
     * the captured contract. A cached page that outlives this must refresh via
     * the media batch endpoint rather than retrying a dead URL.
     */
    val expiresAt: String? = null,
    /** `pending`, `processing`, `ready`, `failed`, `rejected` — the transcode. */
    val processingStatus: String = "",
    /** `pending`, `passed`, `rejected` — moderation. */
    val moderationStatus: String = "",
    /**
     * What to hand a player, when the server chose for us: the HLS master
     * once a rendition exists, or the original file while it does not.
     * Null on rows that predate the instant-reel contract — see [hlsUrl].
     */
    val playbackUrl: String? = null,
    /** `hls` or `original`; blank when the server did not say. */
    val playbackKind: String = "",
    /**
     * The video's length, from the transcode (`duration_ms`, Tube 2026-09-05).
     * Zero for images and for rows from a server that predates the field, so
     * a duration badge is drawn only when there is one to draw.
     */
    val durationMs: Long = 0L,
) {
    val isReady: Boolean get() = status == "ready"

    /** The transcode is still running (or the row said so outright). */
    val isProcessing: Boolean
        get() = processingStatus == "pending" || processingStatus == "processing" ||
            (!isReady && status == "processing")

    /**
     * What a screen reader should announce, or null for silence.
     *
     * Same rule as [PostMediaRef.contentDescription]: a described image is
     * announced, a deliberately decorative one is not, and an undecided legacy
     * asset has nothing to say.
     */
    val contentDescription: String?
        get() = altText.takeIf { it.isNotBlank() && !altDecorative }
    val isVertical: Boolean get() = height > width
}

data class FeedCounts(
    val likes: Int,
    val comments: Int,
    val reposts: Int,
    val views: Int,
)

/**
 * Per-viewer state, now server-authoritative.
 *
 * Previously [hasReacted] and [hasReposted] could only be inferred from taps
 * made in the current session, because the payload carried no per-viewer flag —
 * so a cold start showed every post as un-acted regardless of history. The
 * server now sends all three, and [viewerReaction] names the specific reaction
 * when one is set.
 */
data class FeedViewerState(
    val isBookmarked: Boolean,
    val hasReacted: Boolean,
    val hasReposted: Boolean,
    /**
     * One of `like`, `love`, `haha`, `wow`, `sad`, `angry`. Present only when
     * authenticated AND a reaction is set; absent otherwise. [hasReacted] is
     * the authoritative binary state.
     */
    val viewerReaction: String? = null,
)

/**
 * Which feed surface to load.
 *
 * All four now paginate. Home returns an RFC3339 timestamp cursor; the ranked
 * surfaces return an opaque base64 timeuuid token. Both are replayed verbatim
 * and never parsed or constructed.
 */
enum class FeedSurface(val path: String) {
    Home("home"),
    Reels("reels"),
    Videos("videos"),
    Watch("watch"),
}

/**
 * One request's worth of feed: which surface, and which server-side narrowing
 * of it.
 *
 * Both flags are `GET /v1/feed/home` query parameters (`following_only`,
 * `circle_only`) and both are OFF by default — the server reads either as
 * "the whole home timeline" when absent. They exist as one value rather than
 * two extra parameters on every paging call so the feed's tabs, the Friends
 * tab and reels all describe what they load with a single type, and the
 * paging source stays one class.
 */
data class FeedQuery(
    val surface: FeedSurface,
    /** Only authors the viewer follows — the "Following" tab. */
    val followingOnly: Boolean = false,
    /** Only mutual follows (the viewer's circle) — the Friends tab. */
    val circleOnly: Boolean = false,
) {
    companion object {
        /** The home timeline as the server ranks it — "For You". */
        val ForYou = FeedQuery(FeedSurface.Home)
        val Following = FeedQuery(FeedSurface.Home, followingOnly = true)
        val Friends = FeedQuery(FeedSurface.Home, circleOnly = true)

        /** Reels as the server ranks them — the Reels tab's "For You". */
        val Reels = FeedQuery(FeedSurface.Reels)

        /** Reels from authors the viewer follows — the Reels tab's "Following". */
        val ReelsFollowing = FeedQuery(FeedSurface.Reels, followingOnly = true)

        /** Long videos as the server ranks them — Tube's home (2026-09-05). */
        val Videos = FeedQuery(FeedSurface.Videos)
    }
}

/**
 * One row of `GET /v1/hashtags/trending`.
 *
 * [name] is the normalized tag without its `#` and is what the posts-by-tag
 * endpoint takes; [displayName] is what the server wants shown (`#tag`).
 */
data class TrendingHashtag(
    val name: String,
    val displayName: String,
    val postCount: Long,
) {
    /** Always `#`-prefixed for display, whatever the server sent. */
    val label: String get() = displayName.ifBlank { "#$name" }.let { if (it.startsWith("#")) it else "#$it" }
}
