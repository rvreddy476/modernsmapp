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
 * The server's `normalizeSurface` accepts exactly these six and turns anything
 * else into `"other"`, so an unmapped surface is not an error — it is a lost
 * dimension. Hence an enum: a surface that has no server-side counterpart has
 * to be a deliberate decision at the call site, not a string literal typo.
 */
enum class AnalyticsSurface(val wire: String) {
    FEED("feed"),
    /** The vertical pager. Reported as `feed` until 12 Sep 2026 (audit M-22), which hid the surface. */
    REELS("reels"),
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
 * `LongVideoMilestones` in video_events.go. Sending a short-form session a
 * `VIEW_120S` is accepted by the server but nearly always meaningless — the
 * short-form view rules only apply below `SHORT_FORM_VIEW_BAR_MS`, so a session
 * on that ladder has under ninety seconds to give — hence the ladder is chosen
 * by content type here rather than sending everything.
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
        /**
         * `ReelMilestones.Time` — the ladder stops at 10s because a session
         * scored under the short-form view rules is under ninety seconds long.
         * Note the cap is the *view rule* bar, not the definition of a flick:
         * a flick may run to five minutes, and one that does is scored under
         * the long-form rules and gets the long-form ladder.
         */
        val SHORT_FORM_LADDER = listOf(VIEW_1S, VIEW_3S, VIEW_10S)

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
 * Which milestone ladder a watch session reports against.
 *
 * ## THIS IS NOT WHAT DECIDES A FLICK
 *
 * The platform has exactly one content-type rule and it is the server's:
 * `shared/postclassify` says a video ≤ 300 seconds and portrait or square is a
 * `flick`, everything else a `long_video`. That is what post-service writes on
 * the timeline, what `PostCreated` carries into `analytics.content_ownership`,
 * and what monetization resolves its per-content-type RPM against. `ingest.go`
 * rebuilds [wire] from that projection and drops whatever the client claimed,
 * so nothing here can misclassify a post or misprice a payout.
 *
 * What the ninety seconds below actually is: the *view-counting* bar,
 * `model.ShortFormViewRuleMaxDurationMS`. `IsDisplayView` gives content under
 * it a 3-second / 25% threshold and everything else 30 seconds / 50%. A
 * four-minute flick is still a flick — reels feed, flick RPM — but three
 * seconds of it is not a view, so it is judged by the long-form bar.
 *
 * The client picks a ladder from duration alone because it must choose one
 * before the first milestone fires, long before the server sees anything. Get
 * it wrong and the retention curve is drawn from the wrong rungs; view counts
 * and money are unaffected, because `IsDisplayView` reads `watched_ms_total`
 * and `percent_viewed` off `play_end` and never looks at which milestones
 * arrived.
 */
enum class AnalyticsContentType(val wire: String) {
    /** Short-form. The wire value is the server's vocabulary: `flick`, not `reel`. */
    FLICK("flick"),
    LONG_VIDEO("long_video"),
    ;

    companion object {
        /**
         * `model.ShortFormViewRuleMaxDurationMS`. Ninety seconds, and exactly
         * ninety seconds is still under the bar.
         *
         * Deliberately NOT the flick cap — that is
         * `core.media.publish.REEL_MAX_DURATION_MS`, which is 300 000 and
         * governs what may be published as a reel. The two constants were both
         * called `REEL_MAX_DURATION_MS` until 2026-09-07 and meant different
         * things; if you are reaching for a limit on how long a reel may be,
         * you want the other one.
         */
        const val SHORT_FORM_VIEW_BAR_MS = 90_000L

        /** Which ladder to report against, by duration. See the class doc. */
        fun classify(durationMs: Long): AnalyticsContentType =
            if (durationMs <= SHORT_FORM_VIEW_BAR_MS) FLICK else LONG_VIDEO
    }
}
