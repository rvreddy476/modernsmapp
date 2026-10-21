package com.us.android.core.feed.data

import com.us.android.core.datastore.AudioLanguageStore
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.Playback
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsAudioTrackOption
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.StateFlow
import javax.inject.Inject

/**
 * What the two video players — Reels and Tube's watch screen — share about
 * alternate audio (2026-10-21, as the web's reels stage and watch page do):
 * the tracks of each video, the viewer's ONE language, and the playback a
 * ready dub stands for.
 *
 * A dub is a progressive MP4 with that language muxed in, a single
 * rendition: while one plays the Quality row has Auto alone and goes inert,
 * as the web fixes the quality (its `sourceOverride`). The player keeps its
 * place across the swap — each host's own business.
 */
class VideoAudio @Inject constructor(
    private val repository: AudioTracksRepository,
    private val store: AudioLanguageStore,
    private val urlResolver: MediaUrlResolver,
) {
    /** Media id → its tracks, the original first; absent until read. */
    val tracks: StateFlow<Map<String, List<AudioTrack>>> get() = repository.tracks

    /** The viewer's language, or null for the original. */
    val language: Flow<String?> get() = store.language

    /** Reads [item]'s tracks once; a video with no media id has nothing to read. */
    suspend fun ensure(item: FeedItem) {
        item.videoMediaId()?.let { repository.ensure(it) }
    }

    /** [item]'s tracks as known now: empty until read, and for a video with no media id. */
    fun tracksOf(item: FeedItem, known: Map<String, List<AudioTrack>> = tracks.value): List<AudioTrack> =
        item.videoMediaId()?.let { known[it] }.orEmpty()

    /** The picker's choices for [item]: the original, then each ready dub. */
    fun options(item: FeedItem, known: Map<String, List<AudioTrack>> = tracks.value): List<UsAudioTrackOption> {
        val list = tracksOf(item, known)
        return if (list.isEmpty()) emptyList() else audioTrackOptions(list)
    }

    /**
     * A pick from the "Audio track" row: the language it stands for becomes
     * the viewer's preference, for this video and every one after it.
     * Returns the language stored (null = the original).
     */
    suspend fun choose(item: FeedItem, trackId: String): String? {
        val language = languageForChoice(tracksOf(item), trackId)
        store.setLanguage(language)
        return language
    }

    /**
     * What [item] plays for a viewer who prefers [language]: the ready dub's
     * MP4, resolved against the gateway, or null — the original.
     */
    fun dubPlayback(
        item: FeedItem,
        language: String?,
        known: Map<String, List<AudioTrack>> = tracks.value,
    ): Playback? = dubPlayback(tracksOf(item, known), language, urlResolver::hlsUrl)
}

/**
 * Pure: the ready track in [language] as a progressive playback, its
 * gateway-relative path made absolute by [resolve]; null for the original.
 */
fun dubPlayback(tracks: List<AudioTrack>, language: String?, resolve: (String?) -> String?): Playback? =
    pickAudioTrack(tracks, language)?.playbackUrl?.let(resolve)?.let(Playback::original)

/** The media id of a post's video, which is what audio tracks hang off; null when it has none. */
fun FeedItem.videoMediaId(): String? =
    media.firstOrNull { it.kind == VIDEO_KIND }?.mediaId?.takeIf { it.isNotBlank() }

private const val VIDEO_KIND = "video"
