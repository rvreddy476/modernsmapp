package com.us.android.feature.post.composer

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.post.createhub.VoicePostRequests
import com.us.android.feature.post.data.dto.CreatePostRequest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Test

/**
 * The two new create shapes, as bytes.
 *
 * The same production `Json` configuration as `CreatePostWireTest` —
 * `encodeDefaults` OFF — because that is the setting under which a field
 * equal to its default silently vanishes, and both of these shapes depend on
 * a field that is NOT a default reaching the wire.
 */
class ArticleAndVoiceWireTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private fun CreatePostRequest.wire() =
        json.parseToJsonElement(json.encodeToString(CreatePostRequest.serializer(), this)).jsonObject

    // ── Article ─────────────────────────────────────────────────────────

    @Test
    fun `an article is a text post with its title set`() {
        val state = ComposerUiState(text = "  The body.  ", title = "  A headline  ", longForm = true)

        val obj = ComposerReducer.buildRequest(state).wire()

        assertThat(obj.keys).containsExactly(
            "text",
            "visibility",
            "content_type",
            "post_type",
            "app_origin",
            "media_ids",
            "language",
            "distribution",
            "title",
        )
        assertThat(obj["title"]!!.jsonPrimitive.content).isEqualTo("A headline")
        assertThat(obj["text"]!!.jsonPrimitive.content).isEqualTo("The body.")
        assertThat(obj["content_type"]!!.jsonPrimitive.content).isEqualTo("post")
        assertThat(obj["post_type"]!!.jsonPrimitive.content).isEqualTo("text")
        assertThat(obj["media_ids"]!!.jsonArray).isEmpty()
    }

    /** A short post's bytes are exactly what they were before long-form existed. */
    @Test
    fun `a short post never carries a title even if one is lying around`() {
        val state = ComposerUiState(text = "hi", title = "stale", longForm = false)

        val obj = ComposerReducer.buildRequest(state).wire()

        assertThat(obj.keys).doesNotContain("title")
    }

    @Test
    fun `an article without a title cannot be posted and says why`() {
        val untitled = ComposerUiState(text = "body", longForm = true)
        assertThat(untitled.canPost).isFalse()
        assertThat(untitled.blockedReason).isEqualTo(PostBlockedReason.MissingTitle)

        val titled = untitled.copy(title = "Headline")
        assertThat(titled.canPost).isTrue()

        // Empty body is reported before the missing title: the first thing to
        // fix is the one that gets named.
        val empty = ComposerUiState(longForm = true)
        assertThat(empty.blockedReason).isEqualTo(PostBlockedReason.Empty)
    }

    @Test
    fun `a blank title in article mode sends no title key rather than an empty string`() {
        val state = ComposerUiState(text = "body", title = "   ", longForm = true)
        assertThat(ComposerReducer.buildRequest(state).wire().keys).doesNotContain("title")
    }

    // ── Voice ───────────────────────────────────────────────────────────

    @Test
    fun `a voice post sends content_type voice and the one audio media id`() {
        val obj = VoicePostRequests.build(caption = " a caption ", mediaId = "m-audio-1").wire()

        assertThat(obj.keys).containsExactly(
            "text",
            "visibility",
            "content_type",
            "post_type",
            "app_origin",
            "media_ids",
            "language",
            "distribution",
        )
        assertThat(obj["content_type"]!!.jsonPrimitive.content).isEqualTo("voice")
        assertThat(obj["post_type"]!!.jsonPrimitive.content).isEqualTo("audio")
        assertThat(obj["media_ids"]!!.jsonArray.map { it.jsonPrimitive.content })
            .containsExactly("m-audio-1")
        assertThat(obj["text"]!!.jsonPrimitive.content).isEqualTo("a caption")
        assertThat(obj["visibility"]!!.jsonPrimitive.content).isEqualTo("public")
        assertThat(obj["language"]!!.jsonPrimitive.content).isEqualTo("en")
    }

    /** The caption is optional: the media is the content. */
    @Test
    fun `a voice post with no caption still sends the text key, empty`() {
        val obj = VoicePostRequests.build(caption = "", mediaId = "m1").wire()
        assertThat(obj["text"]!!.jsonPrimitive.content).isEmpty()
        assertThat(obj["media_ids"]!!.jsonArray).hasSize(1)
    }
}
