package com.us.android.feature.post.createhub

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.media.MediaMetadataRetriever
import android.net.Uri
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.feature.post.data.HashtagSearchApi
import com.us.android.feature.post.data.PeopleSearchApi
import com.us.android.feature.post.data.PostCategoriesApi
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.io.ByteArrayOutputStream
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.math.max
import kotlin.math.roundToInt

// ════════════════════════════════════════════════════════════════════════
// Reel form values
// ════════════════════════════════════════════════════════════════════════

/** One entry of the category picker. `id` is what goes on the wire. */
data class ReelCategory(val id: String, val label: String)

/** A person chosen on the "Tag people" screen. */
data class TaggedUser(val id: String, val name: String, val username: String)

/** One row of a picker sheet. [hint] is the quiet second line, when there is one. */
internal data class ReelOption(val value: String, val label: String, val hint: String? = null)

/**
 * One candidate cover: a frame pulled from the video at [timeUs], or — with
 * [index] of [UPLOADED] and no time — an image the user picked from the
 * gallery instead.
 *
 * [bitmap] is null when extraction failed for that position — the strip still
 * shows a slot so the count is stable, and Post falls back to no cover rather
 * than uploading nothing.
 */
data class CoverFrame(val index: Int, val timeUs: Long, val bitmap: Bitmap?) {
    companion object {
        /** The index of a cover that came from the gallery, not the video. */
        const val UPLOADED = -1
    }
}

/**
 * The client's own category list — the founder's set (2026-09-04), used until
 * `GET /v1/posts/categories` answers and preferred over nothing, never over
 * the server. Labels are the ids capitalised; the server's labels win when
 * they load.
 */
val FallbackReelCategories: List<ReelCategory> = listOf(
    "comedy", "music", "dance", "food", "travel", "sports", "education", "tech", "beauty",
    "fashion", "gaming", "fitness", "pets", "art", "news", "lifestyle", "business", "other",
).map { ReelCategory(id = it, label = it.replaceFirstChar(Char::uppercaseChar)) }

/** The most people one reel may tag. */
const val MAX_TAGGED_PEOPLE = 20

// ════════════════════════════════════════════════════════════════════════
// Ports — what the ViewModel needs from the platform and the network
// ════════════════════════════════════════════════════════════════════════

/** Pulls [count] evenly spaced frames from a video. Off-main inside. */
fun interface ReelFrameExtractor {
    suspend fun extract(videoUri: String, count: Int): List<CoverFrame>
}

/**
 * One frame at an exact instant — the cover picker's scrub (founder,
 * 2026-09-05). `OPTION_CLOSEST`, so the frame IS the one under the handle,
 * not the nearest keyframe; off-main inside, on the default dispatcher.
 */
fun interface ReelFrameSeeker {
    suspend fun frameAt(videoUri: String, timeUs: Long): Bitmap?
}

/**
 * A gallery image as a cover: decoded, centre-cropped to [aspect]
 * (width / height) and no longer than 1080 on its long side. Null when the
 * image cannot be read.
 */
fun interface ReelCoverImageLoader {
    suspend fun load(imageUri: String, aspect: Float): Bitmap?
}

/** Turns a chosen frame into the JPEG bytes that get uploaded as the cover. */
fun interface ReelCoverEncoder {
    fun encode(frame: CoverFrame): ByteArray?
}

/** The server lookups the form makes: categories, people search, hashtag suggestions. */
interface ReelLookups {
    /** Null when the endpoint is unavailable, so the form keeps its fallback. */
    suspend fun categories(): List<ReelCategory>?

    suspend fun searchPeople(query: String): List<TaggedUser>

    /** Tags already used on posts that start with [query], most used first; empty when the call fails. */
    suspend fun suggestHashtags(query: String): List<String>
}

// ════════════════════════════════════════════════════════════════════════
// Android implementations
// ════════════════════════════════════════════════════════════════════════

/**
 * `MediaMetadataRetriever` at evenly spaced times — the first frame, then
 * the rest spread over the duration so the last sits just before the end.
 * `OPTION_CLOSEST_SYNC` is what keeps this fast: a keyframe seek, not a
 * decode from the previous keyframe. These are the filmstrip's thumbnails;
 * the cover itself comes from [ReelFrameSeeker] at the exact instant.
 */
@Singleton
class AndroidReelFrameExtractor @Inject constructor(
    @ApplicationContext private val context: Context,
) : ReelFrameExtractor {

    override suspend fun extract(videoUri: String, count: Int): List<CoverFrame> =
        withContext(Dispatchers.IO) {
            val retriever = MediaMetadataRetriever()
            try {
                // MediaMetadataRetriever throws a plain RuntimeException on an
                // unreadable source or a codec failure; either way there is no
                // frame to show, and the strip stays empty.
                runCatching {
                    retriever.setDataSource(context, Uri.parse(videoUri))
                    val durationMs = retriever
                        .extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)
                        ?.toLongOrNull() ?: 0L
                    Filmstrip.timestampsUs(durationMs * MICROS_PER_MILLI, count).mapIndexed { index, timeUs ->
                        val bitmap = runCatching {
                            retriever.getScaledFrameAtTime(
                                timeUs,
                                MediaMetadataRetriever.OPTION_CLOSEST_SYNC,
                                STRIP_MAX_PX,
                                STRIP_MAX_PX,
                            )
                        }.getOrNull()
                        CoverFrame(index = index, timeUs = timeUs, bitmap = bitmap)
                    }
                }.getOrDefault(emptyList())
            } finally {
                runCatching { retriever.release() }
            }
        }

    private companion object {
        const val MICROS_PER_MILLI = 1_000L

        /**
         * Strip thumbnails only: two dozen of them ride in memory at once, and
         * the cover itself is decoded at full size by [ReelFrameSeeker] at the
         * exact instant the handle chose.
         */
        const val STRIP_MAX_PX = 240
    }
}

/**
 * `MediaMetadataRetriever` kept open on the video being scrubbed: opening
 * one per frame costs more than the seek itself, and the picker asks for a
 * frame on every drag tick. One video at a time; a different URI swaps the
 * retriever. Calls are serialized — the retriever is not thread-safe.
 */
@Singleton
class AndroidReelFrameSeeker @Inject constructor(
    @ApplicationContext private val context: Context,
    @Dispatcher(UsDispatcher.Default) private val default: CoroutineDispatcher,
) : ReelFrameSeeker {

    private val lock = Mutex()
    private var openUri: String? = null
    private var retriever: MediaMetadataRetriever? = null

    override suspend fun frameAt(videoUri: String, timeUs: Long): Bitmap? = withContext(default) {
        lock.withLock {
            val source = open(videoUri)
            source?.let {
                runCatching {
                    it.getScaledFrameAtTime(
                        timeUs.coerceAtLeast(0L),
                        MediaMetadataRetriever.OPTION_CLOSEST,
                        COVER_MAX_PX,
                        COVER_MAX_PX,
                    )
                }.getOrNull()
            }
        }
    }

    private fun open(videoUri: String): MediaMetadataRetriever? {
        if (openUri == videoUri) return retriever
        runCatching { retriever?.release() }
        retriever = null
        openUri = null
        val next = MediaMetadataRetriever()
        return runCatching {
            next.setDataSource(context, Uri.parse(videoUri))
            retriever = next
            openUri = videoUri
            next
        }.getOrElse {
            runCatching { next.release() }
            null
        }
    }

    private companion object {
        const val COVER_MAX_PX = 1080
    }
}

/**
 * `BitmapFactory` with a sample size that lands near the cover's size,
 * then a centre crop to the aspect and a scale to 1080 on the long side —
 * the same shape the encoder uploads.
 */
@Singleton
class AndroidReelCoverImageLoader @Inject constructor(
    @ApplicationContext private val context: Context,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : ReelCoverImageLoader {

    override suspend fun load(imageUri: String, aspect: Float): Bitmap? = withContext(io) {
        runCatching { decode(Uri.parse(imageUri))?.let { cropToAspect(it, aspect) } }.getOrNull()
    }

    private fun decode(uri: Uri): Bitmap? {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        context.contentResolver.openInputStream(uri)?.use { BitmapFactory.decodeStream(it, null, bounds) }
        val sample = sampleSize(max(bounds.outWidth, bounds.outHeight))
        val options = BitmapFactory.Options().apply { inSampleSize = sample }
        return context.contentResolver.openInputStream(uri)?.use { BitmapFactory.decodeStream(it, null, options) }
    }

    private fun sampleSize(longest: Int): Int {
        var sample = 1
        while (longest / (sample * 2) >= COVER_MAX_PX) sample *= 2
        return sample
    }

    private fun cropToAspect(source: Bitmap, aspect: Float): Bitmap {
        val width = source.width
        val height = source.height
        val target = if (width.toFloat() / height > aspect) {
            val cropWidth = (height * aspect).roundToInt().coerceIn(1, width)
            Bitmap.createBitmap(source, (width - cropWidth) / 2, 0, cropWidth, height)
        } else {
            val cropHeight = (width / aspect).roundToInt().coerceIn(1, height)
            Bitmap.createBitmap(source, 0, (height - cropHeight) / 2, width, cropHeight)
        }
        val longest = max(target.width, target.height)
        if (longest <= COVER_MAX_PX) return target
        val ratio = COVER_MAX_PX.toFloat() / longest
        return Bitmap.createScaledBitmap(
            target,
            max(1, (target.width * ratio).roundToInt()),
            max(1, (target.height * ratio).roundToInt()),
            true,
        )
    }

    private companion object {
        const val COVER_MAX_PX = 1080
    }
}

/** JPEG at 85%, no edge longer than 1080 — the cover upload's shape. */
@Singleton
class JpegReelCoverEncoder @Inject constructor() : ReelCoverEncoder {

    override fun encode(frame: CoverFrame): ByteArray? {
        val source = frame.bitmap ?: return null
        val longest = max(source.width, source.height)
        val scaled = if (longest > MAX_LONG_EDGE_PX) {
            val ratio = MAX_LONG_EDGE_PX.toFloat() / longest
            Bitmap.createScaledBitmap(
                source,
                max(1, (source.width * ratio).roundToInt()),
                max(1, (source.height * ratio).roundToInt()),
                true,
            )
        } else {
            source
        }
        val out = ByteArrayOutputStream()
        val ok = scaled.compress(Bitmap.CompressFormat.JPEG, JPEG_QUALITY, out)
        if (scaled !== source) scaled.recycle()
        return if (ok) out.toByteArray() else null
    }

    private companion object {
        const val MAX_LONG_EDGE_PX = 1080
        const val JPEG_QUALITY = 85
    }
}

/** The network-backed lookups, through the app-wide Retrofit. */
@Singleton
class ApiReelLookups @Inject constructor(
    private val posts: PostCategoriesApi,
    private val people: PeopleSearchApi,
    private val hashtags: HashtagSearchApi,
    private val errorMapper: ErrorMapper,
) : ReelLookups {

    override suspend fun categories(): List<ReelCategory>? =
        when (val result = apiCall(errorMapper) { posts.categories() }) {
            is AppResult.Success -> {
                result.data
                    .filter { it.id.isNotBlank() }
                    .map { ReelCategory(id = it.id, label = it.label.ifBlank { it.id }) }
                    .takeIf { it.isNotEmpty() }
            }
            is AppResult.Failure -> null
        }

    override suspend fun searchPeople(query: String): List<TaggedUser> =
        when (val result = apiCall(errorMapper) { people.searchUsers(query, PEOPLE_PAGE) }) {
            is AppResult.Success -> {
                result.data.items
                    .filter { it.userId.isNotBlank() }
                    .map {
                        TaggedUser(
                            id = it.userId,
                            name = it.displayName.ifBlank { it.username.ifBlank { "Someone" } },
                            username = it.username,
                        )
                    }
            }
            is AppResult.Failure -> emptyList()
        }

    override suspend fun suggestHashtags(query: String): List<String> =
        when (val result = apiCall(errorMapper) { hashtags.search(query, HASHTAG_PAGE) }) {
            is AppResult.Success ->
                result.data.hashtags.map { it.displayName.ifBlank { it.normalizedName } }.filter { it.isNotBlank() }
            is AppResult.Failure -> emptyList()
        }

    private companion object {
        const val PEOPLE_PAGE = 20
        const val HASHTAG_PAGE = 10
    }
}

@Module
@InstallIn(SingletonComponent::class)
abstract class ReelModule {
    @Binds
    @Singleton
    abstract fun bindFrameExtractor(implementation: AndroidReelFrameExtractor): ReelFrameExtractor

    @Binds
    @Singleton
    abstract fun bindFrameSeeker(implementation: AndroidReelFrameSeeker): ReelFrameSeeker

    @Binds
    @Singleton
    abstract fun bindCoverImageLoader(implementation: AndroidReelCoverImageLoader): ReelCoverImageLoader

    @Binds
    @Singleton
    abstract fun bindCoverEncoder(implementation: JpegReelCoverEncoder): ReelCoverEncoder

    @Binds
    @Singleton
    abstract fun bindLookups(implementation: ApiReelLookups): ReelLookups
}
