package com.us.android.core.feed.offline

import com.us.android.core.media.Playback
import com.us.android.core.media.PlaybackCaption
import com.us.android.core.media.sound.SoundTrack
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedChannel
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.FeedViewerState
import com.us.android.core.model.ORIGINAL_SOUND_TITLE
import com.us.android.core.model.ReelSound
import java.io.File

/*
 * A grant becoming a copy, and a copy becoming what the players take
 * (2026-10-02). A stored copy must open with NO network, so everything the
 * watch screen and the reel's overlay draw is taken from the post as it was
 * when it was saved.
 */

/**
 * The copy to store for [grant], with [item] as it was when the viewer
 * tapped Save offline. The grant's words win where it has them; the row
 * fills what the server left unsaid.
 */
internal fun OfflineGrant.toCopy(item: FeedItem, nowMs: Long, ownerId: String): OfflineCopy {
    val video = item.media.firstOrNull { it.kind == VIDEO_KIND }
    val itemSound = item.sound
    return OfflineCopy(
        postId = postId,
        kind = kind ?: offlineKindOf(item.feedContentType) ?: OfflineKind.VIDEO,
        title = title.ifBlank { item.title.trim().ifBlank { item.text.trim().take(TITLE_FROM_TEXT) } },
        channelName = channelName.ifBlank { item.creatorName },
        durationMs = durationMs.takeIf { it > 0L } ?: video?.durationMs ?: 0L,
        expiresAtMs = expiresAtMs,
        recheckAfterSeconds = recheckAfterSeconds,
        grantedAtMs = nowMs,
        // The grant IS the server's word that the copy may be kept.
        lastCheckedAtMs = nowMs,
        ownerId = ownerId,
        video = OfflineStream(
            streamKey(ownerId, postId, VIDEO_STREAM),
            this.video.url,
            this.video.mime,
            this.video.sizeBytes,
        ),
        sound = sound?.let { granted ->
            OfflineSound(
                stream = OfflineStream(
                    streamKey(ownerId, postId, SOUND_STREAM),
                    granted.stream.url,
                    granted.stream.mime,
                    granted.stream.sizeBytes,
                ),
                startMs = granted.startMs,
                originalVolume = granted.originalVolume,
                overlayVolume = granted.overlayVolume,
                soundId = itemSound?.id.orEmpty(),
                title = itemSound?.title.orEmpty(),
                artist = itemSound?.artist.orEmpty(),
                durationMs = itemSound?.durationMs ?: 0L,
            )
        },
        captions = captions.map { OfflineCaption(language = it.language, label = it.label, url = it.url) },
        post = OfflinePostSnapshot(
            authorId = item.author.id,
            authorName = item.author.displayName,
            authorUsername = item.author.username.orEmpty(),
            avatarMediaId = item.author.avatarMediaId.orEmpty(),
            text = item.text,
            contentType = item.feedContentType,
            width = video?.width ?: 0,
            height = video?.height ?: 0,
            hashtags = item.hashtags,
            channelUserId = item.channel?.userId.orEmpty(),
            channelHandle = item.channel?.handle.orEmpty(),
        ),
    )
}

/** What a player opens for a stored copy: its bytes by key, and its stored caption tracks. */
fun OfflineCopy.playback(): Playback = Playback.offline(
    url = video.url,
    cacheKey = video.key,
    captions = captions.mapNotNull { caption ->
        caption.file?.let { file ->
            PlaybackCaption(uri = File(file).toURI().toString(), language = caption.language, label = caption.label)
        }
    },
)

/**
 * The stored sound, as the sound player takes it: where it starts, and the
 * copy to play it from. Null when the copy has no sound.
 */
fun OfflineCopy.soundTrack(): SoundTrack? = sound?.let { stored ->
    SoundTrack(
        id = stored.soundId,
        startMs = stored.startMs,
        durationMs = stored.durationMs,
        stored = Playback.offline(url = stored.stream.url, cacheKey = stored.stream.key),
    )
}

/**
 * The post, rebuilt from what was kept, for a copy opened with no network.
 * Counts are zero and the viewer's own state is "not": nothing here was
 * asked of the server, and the screens that take this row refresh it from
 * the post detail whenever a network is there.
 */
fun OfflineCopy.toFeedItem(): FeedItem {
    val stored = sound
    return FeedItem(
        id = postId,
        authorId = post.authorId,
        author = FeedAuthor(
            id = post.authorId,
            displayName = post.authorName.ifBlank { channelName },
            username = post.authorUsername.takeIf { it.isNotBlank() },
            avatarMediaId = post.avatarMediaId.takeIf { it.isNotBlank() },
        ),
        text = post.text,
        title = title,
        visibility = "",
        feedContentType = post.contentType.ifBlank { if (kind == OfflineKind.REEL) REEL_TYPE else LONG_VIDEO_TYPE },
        postType = VIDEO_KIND,
        createdAt = "",
        isPinned = false,
        media = listOf(
            FeedMedia(
                mediaId = "",
                kind = VIDEO_KIND,
                status = "ready",
                width = post.width,
                height = post.height,
                durationMs = durationMs,
            ),
        ),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = false,
        channel = post.channelUserId.takeIf { it.isNotBlank() }?.let { userId ->
            FeedChannel(userId = userId, name = channelName, handle = post.channelHandle)
        },
        hashtags = post.hashtags,
        // Only a sound that was known when the reel was saved is named; the mix is the grant's either way.
        sound = stored?.takeIf { it.soundId.isNotBlank() }?.let {
            ReelSound(
                id = it.soundId,
                title = it.title.ifBlank { ORIGINAL_SOUND_TITLE },
                artist = it.artist,
                startMs = it.startMs,
                durationMs = it.durationMs,
                useCount = 0,
                sourcePostId = null,
                creatorUserId = null,
            )
        },
        originalVolume = stored?.originalVolume ?: FULL_VOLUME,
        overlayVolume = stored?.overlayVolume ?: FULL_VOLUME,
    )
}

/**
 * `<owner>/<post>/video`, `<owner>/<post>/sound`: one key per stream, stable
 * for the life of the copy. The owner is in the key because copies now stay
 * on the device for 48 hours after sign-out: a second account saving the
 * same post must get its own bytes, and removing one account's copy must
 * never take the other's.
 */
internal fun streamKey(ownerId: String, postId: String, stream: String): String = "$ownerId/$postId/$stream"

internal const val VIDEO_STREAM = "video"
internal const val SOUND_STREAM = "sound"
private const val VIDEO_KIND = "video"
private const val REEL_TYPE = "flick"
private const val LONG_VIDEO_TYPE = "long_video"
private const val TITLE_FROM_TEXT = 80
private const val FULL_VOLUME = 1.0

/**
 * The stored still, as the app's image loader takes it; null when the copy
 * has none. For the loader only: it is never drawn as text and never leaves
 * the app.
 */
fun OfflineCopy.posterModel(): String? = posterFile?.let { "file://$it" }
