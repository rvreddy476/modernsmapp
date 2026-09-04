package com.us.android.feature.post.createhub

import android.content.Context
import android.media.MediaMetadataRetriever
import android.net.Uri
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.media.publish.VideoKind
import com.us.android.core.media.upload.MediaSourceResolver
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.withContext
import javax.inject.Inject
import javax.inject.Singleton

/**
 * What the form learns about a picked video before anything is uploaded:
 * how long it runs and how many bytes it is. Either is null when the
 * platform could not say — an unreadable header, a provider that reports
 * no size — and an unknown value never blocks a post; the server is the
 * authority and refuses what it must.
 */
data class VideoProbe(val durationMs: Long?, val sizeBytes: Long?)

/** Reads a picked video's duration and size. Off-main inside. */
fun interface ReelVideoProbe {
    suspend fun probe(videoUri: String): VideoProbe
}

/**
 * Whether the picked video may be posted as [VideoKind] (founder, 2026-09-05).
 *
 *  - A REEL is at most five minutes — the server's cap since 2026-09-05.
 *    Anything longer is not refused outright: the form offers to post it as
 *    a long video instead, keeping the selection ([TooLongForReel]).
 *  - A LONG video has no duration cap. Both kinds share the upload ceiling of
 *    500 MB ([TooLarge]); a file over it cannot be helped by switching kind.
 *
 * Pure, so it is a table test. Unknown values ([VideoProbe] nulls) pass:
 * the client must not refuse a post on a fact it could not establish.
 */
sealed interface VideoGate {
    data object Ok : VideoGate

    /** Over the reel cap; the same file is fine as a long video. */
    data class TooLongForReel(val durationMs: Long) : VideoGate

    /** Over the upload ceiling for either kind. */
    data class TooLarge(val sizeBytes: Long) : VideoGate

    val allowsPost: Boolean get() = this is Ok
}

fun videoGate(kind: VideoKind, probe: VideoProbe?): VideoGate {
    val size = probe?.sizeBytes
    if (size != null && size > MAX_UPLOAD_BYTES) return VideoGate.TooLarge(size)
    val duration = probe?.durationMs
    if (kind == VideoKind.REEL && duration != null && duration > REEL_MAX_DURATION_MS) {
        return VideoGate.TooLongForReel(duration)
    }
    return VideoGate.Ok
}

/** The server's shorts cap: five minutes (2026-09-05). */
const val REEL_MAX_DURATION_MS: Long = 5L * 60L * 1_000L

/** The upload ceiling for any video: 500 MB. */
const val MAX_UPLOAD_BYTES: Long = 500L * 1024L * 1024L

/**
 * `MediaMetadataRetriever` for the duration — a header read, not a decode —
 * and the same resolver the upload uses for the size, so the number the
 * gate judges is the number that would go up.
 */
@Singleton
class AndroidReelVideoProbe @Inject constructor(
    @ApplicationContext private val context: Context,
    private val sources: MediaSourceResolver,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : ReelVideoProbe {

    override suspend fun probe(videoUri: String): VideoProbe = withContext(io) {
        VideoProbe(durationMs = readDuration(videoUri), sizeBytes = sources.resolve(videoUri)?.sizeBytes)
    }

    private fun readDuration(videoUri: String): Long? {
        val retriever = MediaMetadataRetriever()
        return try {
            // A plain RuntimeException on an unreadable source: no duration,
            // which the gate reads as "unknown", never as "too long".
            runCatching {
                retriever.setDataSource(context, Uri.parse(videoUri))
                retriever.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLongOrNull()
            }.getOrNull()?.takeIf { it > 0L }
        } finally {
            runCatching { retriever.release() }
        }
    }
}

@Module
@InstallIn(SingletonComponent::class)
abstract class VideoProbeModule {
    @Binds
    @Singleton
    abstract fun bindProbe(implementation: AndroidReelVideoProbe): ReelVideoProbe
}
