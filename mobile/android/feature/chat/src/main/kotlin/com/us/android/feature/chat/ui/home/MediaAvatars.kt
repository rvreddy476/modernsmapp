package com.us.android.feature.chat.ui.home

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.platform.LocalContext
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.data.MediaRepository
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.android.EntryPointAccessors
import dagger.hilt.components.SingletonComponent
import java.util.concurrent.ConcurrentHashMap

/**
 * Resolves a media id to a delivery URL for an avatar or a card image.
 *
 * The chat surfaces receive ids (`avatar_media_id` on a member, a
 * community, a suggestion; `media_ids` on an update) and resolve them
 * through `GET /v1/media/{id}/url` the way every other surface does, never
 * by inventing a URL from the id. A process-wide memo keeps a scrolling
 * list from asking twice for the same asset; signed variants are
 * short-lived, so the memo is by session, not by disk.
 */
@Composable
internal fun rememberMediaUrl(mediaId: String?): String? {
    val context = LocalContext.current
    val url by produceState<String?>(initialValue = mediaId?.let { MediaUrlMemo[it] }, mediaId) {
        val id = mediaId?.takeIf { it.isNotBlank() } ?: return@produceState
        MediaUrlMemo[id]?.let {
            value = it
            return@produceState
        }
        val repository = EntryPointAccessors
            .fromApplication(context.applicationContext, ChatMediaUrlEntryPoint::class.java)
            .mediaRepository()
        val resolved = (repository.delivery(id) as? AppResult.Success)?.data?.posterUrl
        if (resolved != null) {
            MediaUrlMemo[id] = resolved
            value = resolved
        }
    }
    return url
}

private object MediaUrlMemo {
    private val urls = ConcurrentHashMap<String, String>()
    operator fun get(id: String): String? = urls[id]
    operator fun set(id: String, url: String) {
        urls[id] = url
    }
}

@EntryPoint
@InstallIn(SingletonComponent::class)
internal interface ChatMediaUrlEntryPoint {
    fun mediaRepository(): MediaRepository
}
