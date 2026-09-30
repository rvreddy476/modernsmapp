package com.us.android.core.media.sound

/*
 * Keeping an added sound in step with the reel's video: the arithmetic, with
 * no player in sight (original sounds, 2026-09-30).
 *
 * The video and the sound are two files played by two players — nothing is
 * mixed on the server — so on every event that matters something has to say
 * where the sound should be, whether it should be running, and how loud each
 * side is. That something is this file. ReelSoundPlayer only wires events to
 * planSoundSync and copies the answer onto the players: this module decides,
 * the player obeys.
 *
 * It is the web's `soundSync.ts`, rule for rule and constant for constant, so
 * a reel sounds the same on both:
 *
 *  - Position: the sound is at (start offset + video time), wrapped by its
 *    own length when it is shorter than the video, so it loops under a long
 *    reel.
 *  - Drift: the two clocks are never exactly equal. The sound is moved only
 *    when it is further than DRIFT_THRESHOLD_S from where it should be — a
 *    seek is audible, so small differences are left alone. After a jump (a
 *    seek, a loop, a new source) the tighter SNAP_THRESHOLD_S applies,
 *    because the viewer expects the sound to land exactly. The distance is
 *    measured around the loop: 27.9 s and 0.1 s of a 28 s sound are 0.2 s
 *    apart, not 27.8.
 *  - Running: the sound runs only while the video is really advancing.
 *  - Levels: video = viewer x the creator's original level, sound = viewer x
 *    the creator's overlay level. Mute is one switch for both.
 *  - Failure: a sound that cannot be loaded is no sound. The video then plays
 *    at the viewer's FULL level, whatever the creator set for the original —
 *    a reel whose original was turned down to make room for a sound must not
 *    play quietly (or silently) under nothing.
 *
 * Pure Kotlin: no Android, no Media3, no clock and no timer. Times are
 * seconds of media time as Double, as on the web, so the two tables of cases
 * read the same; the player converts from and to milliseconds.
 */

/** How far the sound may wander before it is moved, in seconds of media time. */
const val DRIFT_THRESHOLD_S: Double = 0.3

/** The tolerance right after a jump (seek, loop, source switch). */
const val SNAP_THRESHOLD_S: Double = 0.05

/** A sound still loading after this long is treated as one that failed. */
const val SOUND_LOAD_TIMEOUT_MS: Long = 10_000L

/**
 * A sound that was playing and then fails has most likely outlived its link:
 * `/serve` answers with a signed address that lives five minutes, and a sound
 * left paused for longer is refused when it asks for its next bytes. Asking
 * `/serve` again gets a fresh one. Only a sound that HAD loaded is asked for
 * again, and not sooner than this after it loaded, so a file that is really
 * broken fails once and stays failed instead of reloading in a loop.
 */
const val SOUND_RELOAD_AFTER_MS: Long = 30_000L

/**
 * [NONE]: the reel has no added sound. [LOADING]: it has one, not playable
 * yet. [READY]: playable. [FAILED]: it could not be loaded.
 */
enum class SoundLoad { NONE, LOADING, READY, FAILED }

/** [TICK] is the running clock; [JUMP] is anything that moved it. */
enum class SyncReason { TICK, JUMP }

data class SoundSyncInput(
    /** The video's clock, seconds. */
    val videoTime: Double,
    /** The video is advancing: not paused, not ended, not seeking, not waiting for data. */
    val videoPlaying: Boolean,
    /** The video's playback rate. */
    val rate: Double,
    /** The viewer's volume, 0..1. */
    val viewerVolume: Double,
    /** The viewer muted the reel. */
    val muted: Boolean,
    /** The creator's level for the reel's own audio, 0..1. */
    val originalVolume: Double,
    /** The creator's level for the sound, 0..1. */
    val overlayVolume: Double,
    /** Where in the sound playback starts, seconds. */
    val startOffsetS: Double,
    /** The sound's length, seconds; 0 when unknown (then it cannot wrap). */
    val soundDurationS: Double,
    val load: SoundLoad,
    /** The sound player's clock, seconds. */
    val soundTime: Double,
    /** The sound player is in the middle of a seek of its own: leave it be. */
    val soundSeeking: Boolean = false,
    val reason: SyncReason = SyncReason.TICK,
)

data class SoundCommand(
    /** Whether the sound should be running. */
    val play: Boolean,
    /** Where the sound should be, seconds. */
    val targetTime: Double,
    /** Set when a correction is due: move the sound here. Null = leave its clock alone. */
    val seekTo: Double?,
    val volume: Double,
    val muted: Boolean,
    val rate: Double,
)

data class SoundSyncPlan(
    /** What the video player's volume should be, before the mute switch. */
    val videoVolume: Double,
    /** What the sound player should be set to; null when there is nothing to play. */
    val sound: SoundCommand?,
)

/** What to do with a sound that failed. */
enum class SoundErrorAction { RELOAD, FAIL }

/** What to do when the platform refuses to start the sound. */
enum class PlayRefusedAction { MUTE_BOTH, IGNORE }

private fun finite(value: Double?, fallback: Double = 0.0): Double =
    if (value == null || value.isNaN() || value.isInfinite()) fallback else value

fun clamp01(value: Double): Double = finite(value, 1.0).coerceIn(0.0, 1.0)

/** The sound's length to wrap by: the player's own when it knows it, else what the server declared. */
fun soundDurationS(playerDurationS: Double?, declaredMs: Long?): Double {
    val own = finite(playerDurationS)
    if (own > 0.0) return own
    val declared = declaredMs ?: 0L
    return if (declared > 0L) declared / MILLIS_PER_SECOND else 0.0
}

/** Where the sound should be for a given video time. Wraps when the length is known. */
fun soundPosition(videoTime: Double, startOffsetS: Double, durationS: Double): Double {
    val at = finite(startOffsetS).coerceAtLeast(0.0) + finite(videoTime).coerceAtLeast(0.0)
    val length = finite(durationS)
    if (length <= 0.0) return at
    return at % length
}

/** How far apart two positions of the sound are, measured around the loop. */
fun soundDrift(soundTime: Double, targetTime: Double, durationS: Double): Double {
    val apart = kotlin.math.abs(finite(soundTime) - finite(targetTime))
    val length = finite(durationS)
    if (length <= 0.0) return apart
    val wrapped = apart % length
    return minOf(wrapped, length - wrapped)
}

/** Whether a drift is large enough to move the sound. */
fun correctionDue(drift: Double, reason: SyncReason = SyncReason.TICK): Boolean =
    drift > (if (reason == SyncReason.JUMP) SNAP_THRESHOLD_S else DRIFT_THRESHOLD_S)

/**
 * The video player's volume. The creator's original level applies only while
 * there is a sound to play beside it (loading or ready); with no sound, or
 * one that failed, the viewer's own level is used in full.
 */
fun videoVolume(viewerVolume: Double, originalVolume: Double, load: SoundLoad): Double {
    val viewer = clamp01(viewerVolume)
    if (load == SoundLoad.NONE || load == SoundLoad.FAILED) return viewer
    return viewer * clamp01(originalVolume)
}

fun soundVolume(viewerVolume: Double, overlayVolume: Double): Double = clamp01(viewerVolume) * clamp01(overlayVolume)

private fun safeRate(rate: Double): Double {
    val r = finite(rate, 1.0)
    return if (r > 0.0) r else 1.0
}

/** Everything the two players should be set to, for one event. */
fun planSoundSync(input: SoundSyncInput): SoundSyncPlan {
    val volume = videoVolume(input.viewerVolume, input.originalVolume, input.load)
    if (input.load != SoundLoad.READY) return SoundSyncPlan(videoVolume = volume, sound = null)

    val targetTime = soundPosition(input.videoTime, input.startOffsetS, input.soundDurationS)
    val drift = soundDrift(input.soundTime, targetTime, input.soundDurationS)
    val move = !input.soundSeeking && correctionDue(drift, input.reason)
    return SoundSyncPlan(
        videoVolume = volume,
        sound = SoundCommand(
            play = input.videoPlaying,
            targetTime = targetTime,
            seekTo = if (move) targetTime else null,
            volume = soundVolume(input.viewerVolume, input.overlayVolume),
            muted = input.muted,
            rate = safeRate(input.rate),
        ),
    )
}

/**
 * What to do when the platform refuses to start the sound. Ported from the
 * web, where a browser refuses an unmuted start under its autoplay rule and
 * BOTH sides then go quiet together, so the viewer never hears half of the
 * mix. Android has no such rule — nothing refuses `play` — so
 * ReelSoundPlayer never asks; the rule is kept so the two tables of cases
 * stay the same, and so the answer exists the day a platform rule (audio
 * focus, say) does refuse a start.
 */
fun onSoundPlayRefused(errorName: String?, soundMuted: Boolean): PlayRefusedAction =
    if (errorName == NOT_ALLOWED && !soundMuted) PlayRefusedAction.MUTE_BOTH else PlayRefusedAction.IGNORE

/** A sound that failed: ask for it again, or give up. See [SOUND_RELOAD_AFTER_MS]. */
fun onSoundError(load: SoundLoad, msSinceLoad: Long?): SoundErrorAction =
    if (load == SoundLoad.READY && msSinceLoad != null && msSinceLoad >= SOUND_RELOAD_AFTER_MS) {
        SoundErrorAction.RELOAD
    } else {
        SoundErrorAction.FAIL
    }

// ── Android's additions ─────────────────────────────────────────────────
//
// The three rules below have no twin in soundSync.ts because the web gets
// them from the element: `muted` is a property beside `volume`, `seeking` is
// a property, and the load timeout is a timer. A Media3 player has one
// volume, reports a seek through its state, and this module holds no timer —
// so each is a decision, and decisions live here.

/**
 * The volume a player is actually given. A Media3 player has no mute beside
 * its volume, so the one switch is applied here, to BOTH players: muted is
 * silence, whatever the level.
 */
fun appliedVolume(volume: Double, muted: Boolean): Float = if (muted) 0f else clamp01(volume).toFloat()

/**
 * Whether the sound player is in the middle of a seek of its own. A seek this
 * module asked for is over once the player is no longer buffering; a player
 * buffering for any other reason (the network) is not seeking.
 */
fun soundSeeking(seekIssued: Boolean, buffering: Boolean): Boolean = seekIssued && buffering

/** A sound still loading after [SOUND_LOAD_TIMEOUT_MS] is treated as one that failed. */
fun loadTimedOut(load: SoundLoad, msLoading: Long): Boolean =
    load == SoundLoad.LOADING && msLoading >= SOUND_LOAD_TIMEOUT_MS

private const val MILLIS_PER_SECOND = 1_000.0
private const val NOT_ALLOWED = "NotAllowedError"
