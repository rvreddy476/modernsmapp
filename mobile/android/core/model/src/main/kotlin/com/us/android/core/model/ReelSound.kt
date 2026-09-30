package com.us.android.core.model

/**
 * The added sound a reel plays: the audio of another creator's public reel
 * (original sounds, 2026-09-30).
 *
 * The video and the sound are two files. Nothing is mixed on the server, so
 * this is only what the player needs to play the second one in step — which
 * sound, where in it playback starts, how long it is — and what the screens
 * print. It carries no URL: the bytes are asked for by id and gated on every
 * request, because a sound follows its source video's audience.
 */
data class ReelSound(
    val id: String,
    /** Never blank: a sound the server sent without a title reads [ORIGINAL_SOUND_TITLE]. */
    val title: String,
    val artist: String,
    /** Where in the sound playback starts. */
    val startMs: Long,
    /** 0 when the server did not say; the player then reads it from the file. */
    val durationMs: Long,
    val useCount: Int,
    /** The reel the sound was taken from, when there is one. */
    val sourcePostId: String?,
    /** Who made the source reel, when the server said. */
    val creatorUserId: String?,
)

/** The title a sound shows when the server sent none. */
const val ORIGINAL_SOUND_TITLE = "Original sound"

/**
 * A sound as it arrives, in either service's spelling. post-service says
 * `use_count` and `source_post_id`; media-service's row says `usage_count`
 * and keeps `source_reel_id` beside `source_post_id`. Every field may be
 * absent, and Go sends zero values (`""`, `0`) where a value is unset.
 */
data class ReelSoundWire(
    val id: String? = null,
    val title: String? = null,
    val artist: String? = null,
    val startMs: Long? = null,
    val durationMs: Long? = null,
    val useCount: Long? = null,
    val usageCount: Long? = null,
    val sourcePostId: String? = null,
    val sourceReelId: String? = null,
    val creatorUserId: String? = null,
)

/**
 * A sound off the wire as a [ReelSound], or null when it is no sound at all.
 *
 * The web's `toReelSound`, rule for rule. Go zero values fall through like
 * absent ones: an empty (or blank) id is NO sound, an empty title reads
 * "Original sound", a start of 0 falls back to the post's own
 * `audio_start_ms` ([postStartMs]), `use_count` of 0 falls back to
 * media-service's `usage_count`, an empty source falls back to
 * `source_reel_id` and then to null.
 */
fun ReelSoundWire?.toReelSound(postStartMs: Long? = null): ReelSound? {
    val wire = this ?: return null
    val id = wire.id.text()
    if (id.isEmpty()) return null
    return ReelSound(
        id = id,
        title = wire.title.text().ifEmpty { ORIGINAL_SOUND_TITLE },
        artist = wire.artist.text(),
        startMs = wholeMs(wire.startMs).takeIf { it > 0L } ?: wholeMs(postStartMs),
        durationMs = wholeMs(wire.durationMs),
        useCount = (wholeMs(wire.useCount).takeIf { it > 0L } ?: wholeMs(wire.usageCount)).toCount(),
        sourcePostId = wire.sourcePostId.text().ifEmpty { wire.sourceReelId.text() }.ifEmpty { null },
        creatorUserId = wire.creatorUserId.text().ifEmpty { null },
    )
}

/**
 * A creator's volume off the wire, 0..1.
 *
 * Absent (or not a number) is 1. A PRESENT 0 is a real 0 — the creator muted
 * that side of the mix — so this is the one field where a Go zero value must
 * NOT fall through to the default. Anything outside 0..1 is brought inside.
 */
fun wireVolume(raw: Double?): Double {
    if (raw == null || raw.isNaN() || raw.isInfinite()) return 1.0
    return raw.coerceIn(0.0, 1.0)
}

/**
 * Whether the creator lets others reuse this reel's audio: `remix_setting`
 * is anything but `disallow` (`allow` and `allow_audio_only` both permit it,
 * and so does an empty or unknown value). The author may always reuse their
 * own — [canUseSound] adds that, because the row does not know the viewer.
 */
fun allowsSoundReuse(remixSetting: String?): Boolean = remixSetting.orEmpty().lowercase() != REMIX_DISALLOW

/**
 * Whether "Use this sound" is offered on this reel.
 *
 * A reel that plays an added sound offers THAT sound — the viewer already
 * hears it, whatever the reel's own setting. Otherwise the reel's own audio
 * is offered when the creator allows reuse, or always to its author; never
 * while the reel is still processing, when there is nothing to take yet.
 */
fun FeedItem.canUseSound(isOwn: Boolean): Boolean = when {
    sound != null -> true
    isProcessing -> false
    else -> isOwn || soundReuseAllowed
}

private fun String?.text(): String = this?.trim().orEmpty()

private fun wholeMs(value: Long?): Long = value?.takeIf { it > 0L } ?: 0L

private fun Long.toCount(): Int = coerceAtMost(Int.MAX_VALUE.toLong()).toInt()

private const val REMIX_DISALLOW = "disallow"
