package com.us.android.core.ui

import androidx.compose.runtime.Immutable

/*
 * The video player's choices that the long video's gear sheet and the More
 * sheet of a reel or a long video both offer (TikTok parity T-32,
 * 2026-10-15): the speeds and how a speed is printed, and one caption track.
 * They were Tube's alone (`feature/tube/.../PlayerQuality.kt`); moved here,
 * beside [UsReelQuality], so Reels offers the very same list rather than a
 * second copy that could drift.
 */

/** The speeds offered, in their order — YouTube's six. [SPEED_NORMAL] is the player's rest. */
val PLAYBACK_SPEEDS: List<Float> =
    listOf(SPEED_HALF, SPEED_THREE_QUARTERS, SPEED_NORMAL, SPEED_QUICK, SPEED_QUICKER, SPEED_DOUBLE)

const val SPEED_HALF = 0.5f
const val SPEED_THREE_QUARTERS = 0.75f
const val SPEED_NORMAL = 1f
const val SPEED_QUICK = 1.25f
const val SPEED_QUICKER = 1.5f
const val SPEED_DOUBLE = 2f

/** "1x", "1.25x", "0.5x" — no trailing zeros, the way YouTube prints them. */
fun speedLabel(speed: Float): String {
    val text = speed.toString().trimEnd('0').trimEnd('.')
    return "${text}x"
}

/** One caption track a video offers, by language. What the sheet lists; the player switches by [language]. */
@Immutable
data class UsCaptionOption(val language: String, val label: String)

/** What the Captions row reads at its right: the track that is on, or "Off". */
fun captionValueLabel(options: List<UsCaptionOption>, language: String?): String =
    options.firstOrNull { it.language == language }?.label ?: CAPTIONS_OFF

/** The Captions picker's first option, and the row's value while none is on. */
const val CAPTIONS_OFF = "Off"

/**
 * What the "Audio track" row reads at its right (2026-10-21): the track
 * playing, else the first option — the original, which is what plays when
 * the viewer's language has no ready track here.
 */
fun audioTrackValueLabel(options: List<UsAudioTrackOption>, trackId: String?): String =
    (options.firstOrNull { it.id == trackId } ?: options.firstOrNull())?.label.orEmpty()
