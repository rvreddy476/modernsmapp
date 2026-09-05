package com.us.android.feature.search.data

import com.google.common.truth.Truth.assertThat
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import org.junit.Test

/**
 * The wire shapes, one per result kind, as the 2026-09-05 contract names
 * them — and the older `{"items": […]}` wrapper the people search used,
 * which must keep decoding while the server is finished in parallel.
 */
class SearchDecodingTest {

    private val json = Json { ignoreUnknownKeys = true }

    private fun element(text: String): JsonElement = json.parseToJsonElement(text)

    @Test
    fun `a user row decodes with the new id and the older user_id alike`() {
        val rows = SearchRows.users(
            json,
            element(
                """[
                  {"id":"u1","username":"clee","display_name":"Clee","avatar_url":"https://cdn/a.jpg"},
                  {"user_id":"u2","username":"raj","display_name":"Raj","is_verified":true}
                ]""",
            ),
        )

        assertThat(rows.map { it.key }).containsExactly("u1", "u2").inOrder()
        assertThat(rows[0].avatarUrl).isEqualTo("https://cdn/a.jpg")
        assertThat(rows[1].avatarUrl).isNull()
        assertThat(rows[1].displayName).isEqualTo("Raj")
    }

    @Test
    fun `a wrapped page unwraps through items`() {
        val rows = SearchRows.users(json, element("""{"items":[{"user_id":"u9","username":"nine"}]}"""))

        assertThat(rows).hasSize(1)
        assertThat(rows.single().key).isEqualTo("u9")
    }

    @Test
    fun `a post row carries its author, words and time`() {
        val rows = SearchRows.posts(
            json,
            element(
                """[{"id":"p1","author":{"id":"u1","display_name":"Clee","username":"clee","avatar_url":null},
                     "title":"","text":"hello world","content_type":"post","created_at":"2026-09-05T10:00:00Z"}]""",
            ),
        )

        val row = rows.single()
        assertThat(row.id).isEqualTo("p1")
        assertThat(row.author.displayName).isEqualTo("Clee")
        assertThat(row.author.username).isEqualTo("clee")
        assertThat(row.author.avatarUrl).isNull()
        assertThat(row.text).isEqualTo("hello world")
        assertThat(row.createdAt).isEqualTo("2026-09-05T10:00:00Z")
        assertThat(row.thumbnailUrl).isNull()
        assertThat(row.durationMs).isEqualTo(0L)
    }

    @Test
    fun `a video row carries the still and the length`() {
        val rows = SearchRows.posts(
            json,
            element(
                """[{"id":"v1","author":{"id":"u1","display_name":"Clee"},"title":"My trip",
                     "content_type":"long_video","thumbnail_url":"https://cdn/t.jpg","duration_ms":754000}]""",
            ),
        )

        val row = rows.single()
        assertThat(row.title).isEqualTo("My trip")
        assertThat(row.contentType).isEqualTo("long_video")
        assertThat(row.thumbnailUrl).isEqualTo("https://cdn/t.jpg")
        assertThat(row.durationMs).isEqualTo(754_000L)
    }

    @Test
    fun `a channel row is the channel JSON`() {
        val rows = SearchRows.channels(
            json,
            element(
                """[{"user_id":"u1","name":"Clee Studio","handle":"cleestudio","about":"Long videos",
                     "avatar_url":"https://cdn/c.jpg","video_count":12}]""",
            ),
        )

        val row = rows.single()
        assertThat(row.userId).isEqualTo("u1")
        assertThat(row.name).isEqualTo("Clee Studio")
        assertThat(row.handle).isEqualTo("cleestudio")
        assertThat(row.avatarUrl).isEqualTo("https://cdn/c.jpg")
        assertThat(row.videoCount).isEqualTo(12)
    }

    @Test
    fun `nothing, null and a row that does not decode are an empty or shorter page, never a failure`() {
        assertThat(SearchRows.users(json, null)).isEmpty()
        assertThat(SearchRows.users(json, element("null"))).isEmpty()
        assertThat(SearchRows.users(json, element("""{"total":0}"""))).isEmpty()
        assertThat(SearchRows.users(json, element("""[{"id":"ok"}, "not a row"]"""))).hasSize(1)
    }
}
