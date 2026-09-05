package com.us.android.feature.post.createhub.studio

import android.content.Context
import android.media.MediaMetadataRetriever
import android.net.Uri
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
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
 * What the studio needs to know about a picked video before it can frame
 * it: its DISPLAYED size — rotation metadata applied, so a phone clip shot
 * upright is taller than wide — and its length.
 */
data class ReelSource(
    val uri: String,
    val width: Int,
    val height: Int,
    val durationUs: Long,
)

/** Reads a video's displayed size and length. Null when it cannot be read. Off-main inside. */
fun interface ReelSourceReader {
    suspend fun read(uri: String): ReelSource?
}

/**
 * `MediaMetadataRetriever` for the header: width, height, the rotation
 * that swaps them, and the duration. A header read, not a decode.
 */
@Singleton
class AndroidReelSourceReader @Inject constructor(
    @ApplicationContext private val context: Context,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : ReelSourceReader {

    override suspend fun read(uri: String): ReelSource? = withContext(io) {
        val retriever = MediaMetadataRetriever()
        try {
            // A plain RuntimeException on an unreadable source: no size, no studio.
            runCatching {
                retriever.setDataSource(context, Uri.parse(uri))
                val width = retriever.metadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_WIDTH)
                    ?: return@runCatching null
                val height = retriever.metadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_HEIGHT)
                    ?: return@runCatching null
                val rotation = retriever.metadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_ROTATION) ?: 0L
                val durationMs = retriever.metadata(MediaMetadataRetriever.METADATA_KEY_DURATION) ?: 0L
                if (width <= 0L || height <= 0L || durationMs <= 0L) return@runCatching null
                val sideways = rotation % HALF_TURN != 0L
                ReelSource(
                    uri = uri,
                    width = (if (sideways) height else width).toInt(),
                    height = (if (sideways) width else height).toInt(),
                    durationUs = durationMs * MICROS_PER_MILLI,
                )
            }.getOrNull()
        } finally {
            runCatching { retriever.release() }
        }
    }

    private fun MediaMetadataRetriever.metadata(key: Int): Long? = extractMetadata(key)?.toLongOrNull()

    private companion object {
        const val HALF_TURN = 180L
        const val MICROS_PER_MILLI = 1_000L
    }
}

@Module
@InstallIn(SingletonComponent::class)
abstract class ReelSourceModule {
    @Binds
    @Singleton
    abstract fun bindSourceReader(implementation: AndroidReelSourceReader): ReelSourceReader
}
