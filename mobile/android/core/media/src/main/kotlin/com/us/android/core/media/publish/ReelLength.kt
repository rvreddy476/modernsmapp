package com.us.android.core.media.publish

/**
 * The five-minute line between a reel and a video, in ONE place.
 *
 * The founder's rule (2026-09-06): "to identify long video, which are
 * greater than five minutes — they should not appear in the reels section".
 * That rule has to hold in two modules that may not see each other —
 * `:feature:post`, which refuses to POST a long capture as a reel, and
 * `:feature:feed`, which refuses to DRAW one in the Reels feed — so the cap
 * and the judgement live here, in the module both can see. The constant was
 * `:feature:post`'s (`VideoGate.kt`) until this file; it moved rather than
 * being copied, so there is still exactly one five minutes in the app.
 *
 * Deliberately free of `:core:model`: this module knows nothing about a
 * FeedItem. Callers pass the two facts — the feed's classification and the
 * length the transcode reported — and get an answer.
 */

/** The server's shorts cap: five minutes (2026-09-05). */
const val REEL_MAX_DURATION_MS: Long = 5L * 60L * 1_000L

/** The feed's classification for a long video — Tube's kind. */
const val LONG_VIDEO_CONTENT_TYPE: String = "long_video"

/**
 * Whether a post may appear in the Reels feed.
 *
 * Two ways to fail: it is tagged as a long video, or its video runs past
 * the cap however it was tagged. The second is the defensive half — a post
 * mistagged `flick` by an older client (or by a publish whose duration
 * could not be probed) must not reach Reels just because its label says so.
 *
 * [durationMs] of zero means "not known": images carry none, and so do rows
 * from a server that predates `duration_ms`. Unknown never excludes — the
 * client must not hide a post on a fact it could not establish.
 */
fun playsInReels(contentType: String, durationMs: Long): Boolean =
    contentType != LONG_VIDEO_CONTENT_TYPE && durationMs <= REEL_MAX_DURATION_MS
