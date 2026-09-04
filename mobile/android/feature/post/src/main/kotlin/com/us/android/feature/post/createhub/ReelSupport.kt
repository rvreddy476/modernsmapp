package com.us.android.feature.post.createhub

import android.content.Context
import android.graphics.Bitmap
import android.media.MediaMetadataRetriever
import android.net.Uri
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.feature.post.data.PeopleSearchApi
import com.us.android.feature.post.data.PostCategoriesApi
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.Dispatchers
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
 * One candidate cover: a frame pulled from the video at [timeUs].
 *
 * [bitmap] is null when extraction failed for that position — the strip still
 * shows a slot so the count is stable, and Post falls back to no cover rather
 * than uploading nothing.
 */
data class CoverFrame(val index: Int, val timeUs: Long, val bitmap: Bitmap?)

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

/** Turns a chosen frame into the JPEG bytes that get uploaded as the cover. */
fun interface ReelCoverEncoder {
    fun encode(frame: CoverFrame): ByteArray?
}

/** The two server lookups the form makes: categories and people search. */
interface ReelLookups {
    /** Null when the endpoint is unavailable, so the form keeps its fallback. */
    suspend fun categories(): List<ReelCategory>?

    suspend fun searchPeople(query: String): List<TaggedUser>
}

// ════════════════════════════════════════════════════════════════════════
// Android implementations
// ════════════════════════════════════════════════════════════════════════

/**
 * `MediaMetadataRetriever` at evenly spaced times — the first frame, then
 * the rest spread over the duration so the last sits just before the end.
 * `OPTION_CLOSEST_SYNC` is what keeps this fast: a keyframe seek, not a
 * decode from the previous keyframe.
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
                    val durationUs = durationMs * MICROS_PER_MILLI
                    List(count) { index ->
                        // Evenly spaced across [0, duration), the last one held
                        // back from the very end where many encoders leave a
                        // black frame.
                        val timeUs = if (count <= 1) 0L else durationUs * index / count
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
         * The strip frames are decoded at up to 1080 on the long edge, so the
         * chosen one can be uploaded as the cover without a second decode at
         * full size on Post.
         */
        const val STRIP_MAX_PX = 1080
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

    private companion object {
        const val PEOPLE_PAGE = 20
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
    abstract fun bindCoverEncoder(implementation: JpegReelCoverEncoder): ReelCoverEncoder

    @Binds
    @Singleton
    abstract fun bindLookups(implementation: ApiReelLookups): ReelLookups
}
