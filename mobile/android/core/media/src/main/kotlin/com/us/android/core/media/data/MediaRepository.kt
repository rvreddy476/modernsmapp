package com.us.android.core.media.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Resolves an asset id into something a screen can render.
 *
 * Returns a [MediaDelivery] rather than the DTO so callers never see a raw
 * variants map and cannot pick a rung by hand — choosing the wrong one is how
 * a video rendition ends up in an image loader.
 */
@Singleton
class MediaRepository @Inject constructor(
    private val api: MediaApi,
    private val resolver: MediaUrlResolver,
    private val errorMapper: ErrorMapper,
) {

    suspend fun delivery(mediaId: String): AppResult<MediaDelivery> =
        apiCall(errorMapper) { api.getDelivery(mediaId) }.map { dto ->
            MediaDelivery(
                kind = dto.kind,
                isReady = dto.status == STATUS_READY,
                // A video's ladder rungs are VIDEO files; its only still frame
                // is the thumbnail. Handing a rung to an image loader fetches
                // an mp4 and renders nothing.
                posterUrl = if (dto.kind == KIND_VIDEO) {
                    resolver.thumbnail(dto.variants)
                } else {
                    resolver.bestVariant(dto.variants, MAX_DETAIL_HEIGHT)
                        ?: resolver.thumbnail(dto.variants)
                },
                hlsUrl = resolver.hlsUrl(dto.hlsUrl),
                aspectRatio = if (dto.width > 0 && dto.height > 0) {
                    dto.width.toFloat() / dto.height.toFloat()
                } else {
                    null
                },
            )
        }

    private companion object {
        const val STATUS_READY = "ready"
        const val KIND_VIDEO = "video"

        /**
         * Taller than the feed's budget: a detail screen is the one place a
         * viewer deliberately stops to look, so it is worth the extra bytes.
         */
        const val MAX_DETAIL_HEIGHT = 1080
    }
}

/**
 * What a screen needs to draw one asset.
 *
 * [posterUrl] is null while an asset is still processing — the server sends no
 * variants until then — which is a different thing from an error and must be
 * rendered differently.
 */
data class MediaDelivery(
    val kind: String,
    val isReady: Boolean,
    val posterUrl: String?,
    val hlsUrl: String?,
    val aspectRatio: Float?,
)
