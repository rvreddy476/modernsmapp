package com.us.android.di

import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.CommentsViewer
import com.us.android.core.engagement.data.CommentsViewerSource
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.profile.data.ProfileRepository
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Who the comments composer draws beside the field.
 *
 * Lives in `:app` because it joins three modules that must not know each
 * other: `:core:engagement` declares the seam, `:core:profile` knows who is
 * signed in, and `:core:media` turns an `avatar_media_id` into a URL. The
 * profile payload carries the media ID, never a URL — constructing one from
 * a storage key is exactly what the media capture warns against — so the
 * delivery endpoint is asked, and a still-processing avatar yields null and
 * the initial disc.
 */
@Singleton
class ProfileCommentsViewerSource @Inject constructor(
    private val profiles: ProfileRepository,
    private val media: MediaRepository,
) : CommentsViewerSource {

    override suspend fun current(): CommentsViewer? {
        val profile = when (val result = profiles.getOwnProfile()) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> return null
        }
        val avatarUrl = profile.avatarMediaId?.takeIf { it.isNotBlank() }?.let { mediaId ->
            when (val delivery = media.delivery(mediaId)) {
                is AppResult.Success -> delivery.data.posterUrl.takeIf { delivery.data.isReady }
                is AppResult.Failure -> null
            }
        }
        return CommentsViewer(
            id = profile.userId,
            name = profile.displayName.ifBlank { profile.username },
            avatarUrl = avatarUrl,
        )
    }
}

@Module
@InstallIn(SingletonComponent::class)
abstract class CommentsModule {
    @Binds
    abstract fun bindCommentsViewerSource(impl: ProfileCommentsViewerSource): CommentsViewerSource
}
