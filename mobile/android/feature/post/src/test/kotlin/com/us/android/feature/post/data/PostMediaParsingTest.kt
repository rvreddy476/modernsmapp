package com.us.android.feature.post.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.post.data.dto.PostDto
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * Pins the media reference on the post-detail payload.
 *
 * The body below is a VERBATIM capture from the gateway on 2026-08-19 — the
 * same 1069 bytes the device received. It exists because the screen rendered
 * without its image and made no media request, and the only way to tell a
 * parsing fault from a wiring fault was to decode the real bytes.
 */
class PostMediaParsingTest {

    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun `the post payload's media reference is parsed`() {
        val envelope = json.decodeFromString<ApiEnvelope<PostDto>>(CAPTURED)

        val media = envelope.data?.media.orEmpty()

        assertThat(media).hasSize(1)
        assertThat(media.first().mediaId).isEqualTo("e4484a71-e26d-423a-a179-9ece7f977f11")
        assertThat(media.first().kind).isEqualTo("image")
    }

    private companion object {
        const val CAPTURED = """{"data":{"id":"a742c9a7-acb9-4751-a5b4-0f0a7b7763c8",""" +
            """"author_id":"2d373f48-6d0f-4a62-b439-51dee0b0ec2e",""" +
            """"text":"Followed image fixture for mixed feed","visibility":"public",""" +
            """"content_type":"post","is_pinned":false,"no_comments":false,"no_likes":false,""" +
            """"post_type":"image","app_origin":"postbook","share_to_postbook":true,""" +
            """"review_status":"approved","language":"en","paid_promotion":false,""" +
            """"altered_content":false,"is_made_for_kids":false,"license":"standard",""" +
            """"allow_embedding":true,"publish_to_feed":true,"remix_setting":"allow",""" +
            """"comment_moderation":"none","comment_access":"everyone",""" +
            """"original_audio_volume":1,"overlay_audio_volume":1,""" +
            """"created_at":"2026-08-16T20:21:02.821823Z",""" +
            """"updated_at":"2026-08-16T20:21:02.821823Z",""" +
            """"media":[{"media_id":"e4484a71-e26d-423a-a179-9ece7f977f11","kind":"image"}],""" +
            """"counts":{"likes":0,"comments":0},"view_count":0,"has_reacted":false,""" +
            """"is_bookmarked":false,"repost_count":0,"has_reposted":false,""" +
            """"is_repostable":true}}"""
    }
}
