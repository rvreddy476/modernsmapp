package com.us.android.core.feed.offline

import com.us.android.core.feed.data.hasVideo
import com.us.android.core.media.offline.OfflineFetch
import com.us.android.core.media.offline.OfflineFetchState
import com.us.android.core.model.FeedItem

/*
 * The rules of an offline copy (2026-10-02), each a pure function so it is
 * a table test: who may save, whether the network may be used, whether
 * there is room, whether what arrived is what was promised, and whether a
 * copy may be kept.
 */

// ── Who may save ────────────────────────────────────────────────────────

/**
 * Whether Save offline is offered for [item] to this viewer: a video that
 * is ready, and either the creator allows it (`allow_download`, which now
 * means "viewers may save this offline") or it is the viewer's own. The
 * server decides again on the grant; this only keeps the row off a post it
 * would refuse.
 */
fun FeedItem.canSaveOffline(isOwn: Boolean): Boolean =
    hasVideo() && !isProcessing && !isScheduled && (controls.allowDownload || isOwn)

// ── The network ─────────────────────────────────────────────────────────

/** What the device is connected through. */
enum class OfflineConnection { NONE, METERED, UNMETERED }

/** Whether a fetch may run now. */
enum class OfflineGate { GO, WAIT_FOR_WIFI, WAIT_FOR_NETWORK }

/**
 * Wi-Fi only (the default) holds a fetch on a metered connection: a long
 * video is hundreds of megabytes. Nothing runs with no connection at all.
 */
fun offlineGate(wifiOnly: Boolean, connection: OfflineConnection): OfflineGate = when {
    connection == OfflineConnection.NONE -> OfflineGate.WAIT_FOR_NETWORK
    wifiOnly && connection == OfflineConnection.METERED -> OfflineGate.WAIT_FOR_WIFI
    else -> OfflineGate.GO
}

// ── Room ────────────────────────────────────────────────────────────────

/**
 * Whether a copy of [neededBytes] fits, leaving [STORAGE_RESERVE_BYTES]
 * free: a phone filled to the last byte stops taking photos and receiving
 * messages. A size the server did not declare (0) only has to leave the
 * reserve.
 */
fun hasRoomFor(neededBytes: Long, usableBytes: Long): Boolean =
    usableBytes - neededBytes.coerceAtLeast(0L) >= STORAGE_RESERVE_BYTES

const val STORAGE_RESERVE_BYTES = 200L * 1024 * 1024

// ── What arrived ────────────────────────────────────────────────────────

/**
 * Whether the stored bytes are the stream that was granted.
 *
 * [expectedBytes] is the grant's `size_bytes`, the exact length when
 * present; without it the length the storage host declared while serving
 * ([declaredBytes]) stands in. With neither there is nothing to compare
 * against and any non-empty stream passes. A mismatch means a truncated or
 * substituted file, and the copy is discarded rather than played.
 */
fun sizeMatches(expectedBytes: Long, declaredBytes: Long, storedBytes: Long): Boolean {
    if (storedBytes <= 0L) return false
    val wanted = expectedBytes.takeIf { it > 0L } ?: declaredBytes.takeIf { it > 0L } ?: return true
    return storedBytes == wanted
}

// ── Keeping a copy ──────────────────────────────────────────────────────

/** What to do with a copy after looking at the clock and the server's answer. */
sealed interface OfflineVerdict {
    /** Nothing was learned; the copy stays as it is. */
    data object Keep : OfflineVerdict

    /** The server said it may be kept; remember when, and its expiry. */
    data class Confirmed(val expiresAtMs: Long) : OfflineVerdict

    /** The copy is deleted. [reason] is the server's token, or `expired`. */
    data class Delete(val reason: String) : OfflineVerdict
}

/**
 * The one decision about keeping a copy.
 *
 *  - Past its expiry the copy goes, whatever the server says and with no
 *    network at all: the grant was for thirty days.
 *  - NO ANSWER IS NOT A REVOCATION. A check that could not be made (no
 *    network, a 5xx) or that did not mention this post leaves the copy
 *    alone until its expiry. A phone in flight mode keeps its videos.
 *  - "Not valid" from the server deletes it, whatever the reason.
 *  - "Valid" keeps it and takes the server's expiry, which a check never
 *    extends (only a repeated grant does: `OfflineCopies.renew`).
 */
fun offlineVerdict(copy: OfflineCopy, nowMs: Long, answer: OfflineCheckAnswer?): OfflineVerdict = when {
    nowMs >= copy.expiresAtMs -> OfflineVerdict.Delete(REASON_EXPIRED)
    answer == null -> OfflineVerdict.Keep
    answer is OfflineCheckAnswer.Invalid -> OfflineVerdict.Delete(answer.reason)
    answer is OfflineCheckAnswer.Valid -> {
        val expires = answer.expiresAtMs ?: copy.expiresAtMs
        if (nowMs >= expires) OfflineVerdict.Delete(REASON_EXPIRED) else OfflineVerdict.Confirmed(expires)
    }
    else -> OfflineVerdict.Keep
}

/** Whether the server's last word on [copy] is older than it said an answer is good for. */
fun recheckDue(copy: OfflineCopy, nowMs: Long): Boolean =
    nowMs - copy.lastCheckedAtMs >= copy.recheckAfterSeconds * MILLIS_PER_SECOND

// ── Renewing a copy ─────────────────────────────────────────────────────

/**
 * Whether a renewal may be tried for [copy] now: at most once in
 * [RENEW_INTERVAL_MS], counted from the grant or from the last try,
 * whichever is later, and whatever the last try came to. A copy saved an
 * hour ago is not renewed, and a refusal is not asked again all day.
 */
fun renewDue(copy: OfflineCopy, nowMs: Long): Boolean =
    nowMs - maxOf(copy.grantedAtMs, copy.lastRenewAtMs) >= RENEW_INTERVAL_MS

const val RENEW_INTERVAL_MS = 24L * 60 * 60 * 1000

// ── After sign-out ──────────────────────────────────────────────────────

/**
 * Whether copies whose owner signed out at [signedOutAtMs] have been held
 * for the whole of [SIGN_OUT_HOLD_MS] and must now be deleted.
 */
fun signOutHoldOver(signedOutAtMs: Long, nowMs: Long): Boolean = nowMs - signedOutAtMs >= SIGN_OUT_HOLD_MS

/** founder, 2026-10-02: copies stay 48 hours after sign-out, for the same account to come back to. */
const val SIGN_OUT_HOLD_MS = 48L * 60 * 60 * 1000

/** A stored copy that may be played right now: whole, and not past its expiry. */
fun OfflineCopy.isPlayable(nowMs: Long): Boolean = stored && nowMs < expiresAtMs

// ── Progress ────────────────────────────────────────────────────────────

/** How a copy's fetches stand together. */
sealed interface OfflineFetchSummary {
    /** Every stream arrived. */
    data object Done : OfflineFetchSummary

    /** A stream was given up on. */
    data object Failed : OfflineFetchSummary

    /** Still arriving, or held. [progress] is null while no length is known. */
    data class Running(val progress: Float?, val waiting: Boolean) : OfflineFetchSummary
}

/**
 * One copy's streams, read together: failed if any failed, done only when
 * all are, else running with the bytes of all against the sizes of all. A
 * stream with no fetch yet counts as not started. The size is the grant's
 * when it gave one, the fetch's own otherwise.
 */
fun summarizeFetches(streams: List<OfflineStream>, fetches: Map<String, OfflineFetch>): OfflineFetchSummary {
    val states = streams.map { fetches[it.key] }
    if (states.any { it?.state == OfflineFetchState.FAILED }) return OfflineFetchSummary.Failed
    if (states.isNotEmpty() && states.all { it?.state == OfflineFetchState.DONE }) return OfflineFetchSummary.Done
    var bytes = 0L
    var total = 0L
    var sized = true
    streams.forEachIndexed { index, stream ->
        val fetch = states[index]
        val size = stream.sizeBytes.takeIf { it > 0L } ?: fetch?.totalBytes?.takeIf { it > 0L }
        if (size == null) sized = false else total += size
        bytes += fetch?.bytes ?: 0L
    }
    val progress = if (sized && total > 0L) (bytes.toFloat() / total).coerceIn(0f, 1f) else null
    val waiting = states.none { it?.state == OfflineFetchState.RUNNING } &&
        states.any { it?.state == OfflineFetchState.WAITING }
    return OfflineFetchSummary.Running(progress, waiting)
}

private const val MILLIS_PER_SECOND = 1_000L
