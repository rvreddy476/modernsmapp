package com.us.android.feature.feed.ui.reels

import com.us.android.core.feed.data.originalSoundLabel
import com.us.android.core.media.ChosenSound
import com.us.android.core.media.sound.SoundMix
import com.us.android.core.media.sound.SoundTrack
import com.us.android.core.model.FeedItem
import com.us.android.core.model.ReelSound
import com.us.android.core.model.canUseSound

/*
 * A reel's sound, as the Reels screen reads it (original sounds, 2026-09-30):
 * what the sound line says, what "use this sound" does next, and what the
 * sound player is handed. Pure, so each rule is a table test without a
 * player, a pager or a server.
 */

/** Where "use this sound" ends: the reel create flow with the sound chosen, or the sound's own page. */
enum class SoundIntent { CREATE, PAGE }

/** The next step when a viewer asks for a reel's sound. */
sealed interface SoundStep {
    /** The reel already plays an added sound: it is known, go there. */
    data class Open(val sound: ReelSound) : SoundStep

    /** The reel's own audio: ask the server for its sound first (`POST v1/posts/{id}/sound`). */
    data class Resolve(val postId: String) : SoundStep
}

/**
 * A reel that plays an added sound offers THAT sound, and nothing is asked
 * of the server — the viewer already hears it. One that plays only its own
 * audio has its sound made on first use.
 */
fun nextSoundStep(item: FeedItem): SoundStep =
    item.sound?.let { SoundStep.Open(it) } ?: SoundStep.Resolve(item.id)

/** Where the screen goes once the sound is known. */
sealed interface SoundDestination {
    /** The reel create flow; the sound is already waiting for it in `SoundEntry`. */
    data object Create : SoundDestination

    /** The sound's own page. */
    data class Page(val soundId: String) : SoundDestination
}

fun soundDestination(intent: SoundIntent, sound: ReelSound): SoundDestination = when (intent) {
    SoundIntent.CREATE -> SoundDestination.Create
    SoundIntent.PAGE -> SoundDestination.Page(sound.id)
}

/** A sound as the create flow holds it. A chosen sound starts at 0: there is no offset picker. */
fun ReelSound.toChosenSound() = ChosenSound(id = id, title = title, artist = artist, durationMs = durationMs)

/**
 * The sound line under a reel's hashtags: a note and a name.
 *
 * @property label what it reads.
 * @property added true when the reel plays an ADDED sound — the line then
 *   opens that sound's page. False when it names the reel's own audio, and
 *   a tap makes the sound first ("use this sound") and then opens its page.
 */
data class SoundLine(val label: String, val added: Boolean)

/**
 * What the sound line says, or null when there is no line.
 *
 * A reel that plays an added sound names it. One that plays only its own
 * audio reads "Original sound - <creator>" — but only when that audio may be
 * reused (the creator allows it, or the reel is the viewer's own) and the
 * reel is not still processing: a line that cannot be followed is
 * decoration pretending to be information.
 */
fun soundLine(item: FeedItem, isOwn: Boolean): SoundLine? {
    item.sound?.let { return SoundLine(label = it.title, added = true) }
    if (!item.canUseSound(isOwn)) return null
    return SoundLine(label = originalSoundLabel(item.creatorName), added = false)
}

/** Whether [item] is the viewer's own: a known viewer, and the same author. */
fun FeedItem.isOwnedBy(viewerId: String): Boolean = viewerId.isNotBlank() && author.id == viewerId

/** What the sound player is handed for a reel: which sound and where it starts, or null when it plays none. */
fun FeedItem.soundTrack(): SoundTrack? =
    sound?.let { SoundTrack(id = it.id, startMs = it.startMs, durationMs = it.durationMs) }

/** The creator's two levels for a reel. */
fun FeedItem.soundMix() = SoundMix(originalVolume = originalVolume, overlayVolume = overlayVolume)

/** A hashtag as its chip prints it: one `#`, whatever the row sent. */
fun hashtagLabel(tag: String): String = "#" + tag.trim().trimStart('#')

/** The hashtags a reel's chips show: no blanks, nothing twice, in the author's order. */
fun reelHashtags(item: FeedItem): List<String> = item.hashtags
    .map { it.trim().trimStart('#') }
    .filter { it.isNotEmpty() }
    .distinctBy { it.lowercase() }

/**
 * The reel's title, or null when there is none to draw: a reel made before
 * titles has none, and one whose title only repeats its caption is drawn
 * once, as the caption.
 */
fun reelTitle(item: FeedItem): String? = item.title.trim().takeIf { it.isNotEmpty() && it != item.text.trim() }
