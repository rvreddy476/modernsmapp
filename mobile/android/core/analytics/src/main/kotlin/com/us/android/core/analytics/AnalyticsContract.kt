package com.us.android.core.analytics

/**
 * The wire vocabulary of `POST /v1/analytics/events`.
 *
 * ## READ OFF THE SERVICE, NOT INVENTED
 *
 * Every constant here was taken from
 * `Architecture/services/analytics-service/internal/model/video_events.go` and
 * `internal/service/ingest.go` (as of 2026-09-07). Where the server normalises
 * an unknown value to `"other"` rather than rejecting it, that is noted — those
 * are the places a typo here would silently destroy a dimension rather than
 * fail loudly, so the values are enums and never free strings.
 *
 * ## THE ONE RULE THAT SHAPES THE WHOLE CLIENT
 *
 * `IngestEvents` validates events in a loop and returns on the FIRST failure,
 * which fails the WHOLE batch — every valid event travelling with an invalid
 * one is rejected too. So this client validates locally before enqueueing
 * ([AnalyticsValidation]), and the uploader isolates a rejected batch by
 * bisection rather than retrying or dropping it wholesale.
 */
object AnalyticsEventType {
    const val IMPRESSION = "impression"
    const val PLAY_START = "play_start"
    const val WATCH_HEARTBEAT = "watch_heartbeat"
    const val MILESTONE = "milestone"
    const val PLAY_END = "play_end"
    const val LIKE = "like"
    const val COMMENT_CREATE = "comment_create"
    const val SHARE = "share"
    const val SAVE = "save"
    const val FOLLOW_FROM_CONTENT = "follow_from_content"
    const val NOT_INTERESTED = "not_interested"
    const val REPORT = "report"
    const val BLOCK_CREATOR = "block_creator"

    /**
     * The types the server collapses to one row per (actor, session, content,
     * type) — `oncePerSession` in ingest.go. The client mirrors it so a second
     * tap never occupies a queue slot.
     */
    val ONCE_PER_SESSION = setOf(
        LIKE,
        SHARE,
        SAVE,
        FOLLOW_FROM_CONTENT,
        NOT_INTERESTED,
        REPORT,
        BLOCK_CREATOR,
    )

    /** The types the server refuses without a session uuid — `requiresSession`. */
    val REQUIRES_SESSION = setOf(PLAY_START, WATCH_HEARTBEAT, MILESTONE, PLAY_END)
}

/**
 * Where the content was being watched.
 *
 * The server's `normalizeSurface` accepts exactly these five and turns anything
 * else into `"other"`, so an unmapped surface is not an error — it is a lost
 * dimension. Hence an enum: a surface that has no server-side counterpart has
 * to be a deliberate decision at the call site, not a string literal typo.
 */
enum class AnalyticsSurface(val wire: String) {
    FEED("feed"),
    POSTTUBE("posttube"),
    PROFILE("profile"),
    SEARCH("search"),
    CHANNEL("channel"),
}

/** `normalizeStartMethod` — anything else becomes `"other"`. */
enum class PlayStartMethod(val wire: String) {
    AUTOPLAY("autoplay"),
    TAP("tap"),
    RESUME("resume"),
}

/** `normalizeEndReason` — anything else becomes `"other"`. */
enum class PlayEndReason(val wire: String) {
    ENDED("ended"),
    SWIPE_NEXT("swipe_next"),
    PAUSED("paused"),
    BACKGROUNDED("backgrounded"),
    ERROR("error"),
}

/**
 * `normalizeNegativeReason` — anything else becomes `"unspecified"`.
 *
 * Deliberately a closed set: the server bounds it so a client cannot use the
 * analytics pipe as free-text storage, and an open string would also carry
 * whatever a user typed into a report form into an aggregation table.
 */
enum class NegativeSignalReason(val wire: String) {
    SPAM("spam"),
    NUDITY("nudity"),
    VIOLENCE("violence"),
    HATE("hate"),
    MISINFORMATION("misinformation"),
    REPETITIVE("repetitive"),
    IRRELEVANT("irrelevant"),
    DISLIKE_CREATOR("dislike_creator"),
    UNSPECIFIED("unspecified"),
}

/**
 * The milestone thresholds, and the rules for which apply to what.
 *
 * `validMilestone` in ingest.go accepts the union of both time ladders plus the
 * four percent steps; the split between them comes from `ReelMilestones` and
 * `LongVideoMilestones` in video_events.go. Sending a reel a `VIEW_120S` is
 * accepted by the server but meaningless — a reel is at most 90 seconds — so
 * the ladder is chosen by content type here rather than sending everything.
 */
// The thresholds ARE the contract — VIEW_30S is thirty seconds and nothing
// else — so naming each one a constant would only add a second place to read.
@Suppress("MagicNumber")
enum class WatchMilestone(val wire: String, val thresholdMs: Long) {
    VIEW_1S("VIEW_1S", 1_000),
    VIEW_3S("VIEW_3S", 3_000),
    VIEW_10S("VIEW_10S", 10_000),
    VIEW_30S("VIEW_30S", 30_000),
    VIEW_60S("VIEW_60S", 60_000),
    VIEW_120S("VIEW_120S", 120_000),
    ;

    companion object {
        /** `ReelMilestones.Time` — a reel is capped at 90s, so the ladder stops at 10s. */
        val REEL_LADDER = listOf(VIEW_1S, VIEW_3S, VIEW_10S)

        /** `LongVideoMilestones.Time`. */
        val LONG_VIDEO_LADDER = listOf(VIEW_10S, VIEW_30S, VIEW_60S, VIEW_120S)
    }
}

/** The percent ladder, identical for both content types. */
@Suppress("MagicNumber") // PCT_25 is twenty-five percent; a constant would only restate the name.
enum class PercentMilestone(val wire: String, val percent: Int) {
    PCT_25("PCT_25", 25),
    PCT_50("PCT_50", 50),
    PCT_75("PCT_75", 75),
    PCT_95("PCT_95", 95),
}

/**
 * Reel or long video.
 *
 * ## THE 90-SECOND BOUNDARY IS THE SERVER'S, NOT OURS
 *
 * `model.ClassifyContentType` is `durationMS <= 90000 -> reel`, i.e. exactly
 * ninety seconds is still a reel. It matters well beyond a label: `IsDisplayView`
 * gives reels a 3-second / 25% bar and long video a 30-second / 50% one, so a
 * video misclassified at the boundary is counted under the wrong view rule and
 * the creator is paid on the wrong basis.
 *
 * The client classifies too, because it must choose a milestone ladder before
 * the server ever sees the event. The server re-derives content type from its
 * own ownership projection and ignores whatever the client claims, so the two
 * cannot disagree in the stored row — but they must agree here or the
 * milestones are drawn from the wrong ladder.
 */
enum class AnalyticsContentType(val wire: String) {
    REEL("reel"),
    LONG_VIDEO("long_video"),
    ;

    companion object {
        const val REEL_MAX_DURATION_MS = 90_000L

        /** Mirrors `model.ClassifyContentType`: `<= 90000` is a reel. */
        fun classify(durationMs: Long): AnalyticsContentType =
            if (durationMs <= REEL_MAX_DURATION_MS) REEL else LONG_VIDEO
    }
}
