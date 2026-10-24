package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.model.VideoChapter
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import org.junit.Test

/**
 * Chapters on the post detail (2026-10-24): `chapters: [{start_ms, title}]`
 * (post-service service/post.go:599, ChapterRef at service/chapters.go:30),
 * always an array on `GET /v1/posts/:id`, null on list surfaces.
 */
class ChaptersWireTest {

    private val json = NetworkModule.provideJson()

    private fun decode(body: String): FeedItemDto = json.decodeFromString<ApiEnvelope<FeedItemDto>>(body).data!!

    @Test
    fun `the detail's chapters reach the row sorted and cleaned`() {
        val row = decode(
            """{"data":{"id":"v1","content_type":"long_video","chapters":[
                {"start_ms":65000,"title":" Setup "},
                {"start_ms":0,"title":""},
                {"start_ms":65000,"title":"repeat"},
                {"title":"no start"}
            ]}}""",
        ).toDomain()

        assertThat(row.chapters).containsExactly(VideoChapter(0, "0:00"), VideoChapter(65_000, "Setup")).inOrder()
    }

    @Test
    fun `a list row's null and an absent key are no chapters`() {
        assertThat(decode("""{"data":{"id":"v1","chapters":null}}""").toDomain().chapters).isEmpty()
        assertThat(decode("""{"data":{"id":"v1"}}""").toDomain().chapters).isEmpty()
        assertThat(decode("""{"data":{"id":"v1","chapters":[]}}""").toDomain().chapters).isEmpty()
    }
}
