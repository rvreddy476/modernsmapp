package com.us.android.feature.post.createhub

import com.us.android.feature.post.data.dto.CONTENT_TYPE_VOICE
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.DistributionRequest
import com.us.android.feature.post.data.dto.POST_TYPE_AUDIO

/**
 * The bytes a voice post sends — pure, so the wire shape is a table test
 * rather than something only a device run can show.
 *
 * `content_type: voice` is in post-service's `validContentTypes`
 * (`internal/service/post.go:562`); the server also re-derives it from the
 * attached asset's kind (`post.go:944`), so a confirmed AUDIO media id is the
 * thing that actually makes this a voice post. The caption is the post text
 * and may be empty: the media is the content.
 */
object VoicePostRequests {

    fun build(caption: String, mediaId: String, language: String = DEFAULT_LANGUAGE): CreatePostRequest =
        CreatePostRequest(
            text = caption.trim(),
            contentType = CONTENT_TYPE_VOICE,
            postType = POST_TYPE_AUDIO,
            mediaIds = listOf(mediaId),
            language = language,
            distribution = DistributionRequest(),
        )

    private const val DEFAULT_LANGUAGE = "en"
}
