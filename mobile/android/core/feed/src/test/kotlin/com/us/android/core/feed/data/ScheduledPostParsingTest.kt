package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.dto.FeedItemDto
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * The details-step fields on a row (2026-09-05): `hashtags`, `mentions`,
 * `publish_at` and `is_scheduled` parse when present and default when a
 * server predates them, so every existing fixture still maps.
 */
class ScheduledPostParsingTest {

    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun `a scheduled row carries its instant, its flag, its chips and its mentions`() {
        val dto = json.decodeFromString<FeedItemDto>(
            """
            {"id":"p1","author_id":"a","content_type":"flick","text":"no tags here",
             "hashtags":["longboard","sunday"],"mentions":["maya"],
             "publish_at":"2026-09-06T13:00:00Z","is_scheduled":true}
            """.trimIndent(),
        )

        val item = dto.toDomain()

        assertThat(item.hashtags).containsExactly("longboard", "sunday").inOrder()
        assertThat(item.mentions).containsExactly("maya")
        assertThat(item.publishAt).isEqualTo("2026-09-06T13:00:00Z")
        assertThat(item.isScheduled).isTrue()
        assertThat(item.feedContentType).isEqualTo("flick")
    }

    @Test
    fun `a row from before the fields existed has none of them`() {
        val item = json.decodeFromString<FeedItemDto>("""{"id":"p1","author_id":"a","content_type":"post"}""")
            .toDomain()

        assertThat(item.hashtags).isEmpty()
        assertThat(item.mentions).isEmpty()
        assertThat(item.publishAt).isNull()
        assertThat(item.isScheduled).isFalse()
    }

    @Test
    fun `blank chips and a blank instant are dropped rather than kept as empty strings`() {
        val item = json.decodeFromString<FeedItemDto>(
            """{"id":"p1","author_id":"a","hashtags":["", "ok"],"mentions":[""],"publish_at":""}""",
        ).toDomain()

        assertThat(item.hashtags).containsExactly("ok")
        assertThat(item.mentions).isEmpty()
        assertThat(item.publishAt).isNull()
    }
}
