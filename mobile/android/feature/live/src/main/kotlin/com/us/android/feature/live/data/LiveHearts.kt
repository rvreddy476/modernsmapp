package com.us.android.feature.live.data

import com.us.android.core.common.error.AppError

/*
 * Free hearts (live surfaces contract, 2026-10-02). No money anywhere: a
 * heart is a free tap. There are no gifts, coins or paid anything in live
 * (founder decision) — this is the one viewer reaction besides chat.
 *
 *   POST /v1/livestream/streams/:id/hearts {"count": n}, n 1..20, about once a second
 *        → {"data":{"heart_count": <stream total>}}
 *        401 signed out · 403 BANNED_FROM_STREAM | LIVE_BANNED · 409 STREAM_NOT_LIVE
 *        429 RATE_LIMITED (60 hearts / 10 s / viewer / stream)
 *   frame `hearts` {count, heart_count}: aggregated, at most one a second, names nobody
 */

/** The largest batch one request may carry. */
const val HEART_BATCH_MAX = 20

/** Taps are gathered for this long before they are sent. */
const val HEART_FLUSH_MILLIS = 1_000L

/** Why hearts cannot be sent right now. */
enum class HeartBlock { SignedOut, Banned, NotLive }

/** The batch to send for [pending] taps: 0 for nothing, never more than [HEART_BATCH_MAX]. */
fun heartsBatch(pending: Int): Int = pending.coerceIn(0, HEART_BATCH_MAX)

/** Hearts are open while the stream is on air. */
fun heartsOpen(status: LiveStatus): Boolean = status == LiveStatus.Live || status == LiveStatus.Reconnecting

/**
 * What a refused batch means: a reason to stop sending, or null to drop the
 * batch quietly (429, a network blip, anything unexpected). Never re-sent,
 * so a refusal cannot loop.
 */
fun heartBlockFor(error: AppError): HeartBlock? = when {
    error.liveCode() == CODE_BANNED_FROM_STREAM || error.liveCode() == CODE_LIVE_BANNED -> HeartBlock.Banned
    error.liveCode() == CODE_STREAM_NOT_LIVE -> HeartBlock.NotLive
    error is AppError.AuthFailed -> HeartBlock.SignedOut
    else -> null
}

/**
 * The total after a `hearts` frame: the frame's total when it carried one
 * (never going down), else the old total plus everybody's new hearts.
 */
fun heartTotalAfterFrame(current: Long, frame: LiveFrame.Hearts): Long =
    if (frame.heartCount > 0) maxOf(current, frame.heartCount) else current + frame.count.coerceAtLeast(0)

const val CODE_BANNED_FROM_STREAM = "BANNED_FROM_STREAM"
const val CODE_LIVE_BANNED = "LIVE_BANNED"
const val CODE_STREAM_NOT_LIVE = "STREAM_NOT_LIVE"
