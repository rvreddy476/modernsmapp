package com.us.android.core.media

import androidx.media3.common.C
import androidx.media3.common.Player

/**
 * The caption track to show, by language, or none (offline copies,
 * 2026-10-02). Off disables the text renderer outright, so a track that
 * happens to be flagged default is not drawn against the viewer's choice.
 * Unlike a quality change this needs no re-prepare: a text track is
 * switched without touching the video decoder.
 *
 * Here since T-32 (2026-10-15), moved from Tube's watch screen, so Reels
 * switches captions the same way the long video does.
 */
fun Player.applyCaption(language: String?) {
    val before = trackSelectionParameters
    val after = before.buildUpon()
        .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, language == null)
        .setPreferredTextLanguage(language)
        .build()
    if (after != before) trackSelectionParameters = after
}

/**
 * The viewer's caption choice carried to the next video: kept when that
 * video has the same language, off when it does not. Captions never turn
 * themselves on.
 */
fun captionChoice(current: String?, offered: List<String>): String? = current?.takeIf { it in offered }
