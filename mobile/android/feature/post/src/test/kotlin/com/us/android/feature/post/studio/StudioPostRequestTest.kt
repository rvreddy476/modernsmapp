package com.us.android.feature.post.studio

import com.google.common.truth.Truth.assertThat
import com.us.android.core.creator.model.PostDetails
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.DistributionRequest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Test

/**
 * The Studio's create request with the details screen's choices
 * (2026-10-18). The bytes are FROZEN for replay, so the two promises that
 * matter are pinned: no details is the request this froze before details
 * existed, byte for byte; and each choice lands under the server's name.
 */
class StudioPostRequestTest {

    /** Mirrors the transport's own `Json` (and `NetworkModule`'s on the fields that matter): no defaults encoded. */
    private val json = Json { ignoreUnknownKeys = false }

    private val media = listOf("m-1", "m-2", "m-3")

    private fun encode(request: CreatePostRequest) = json.encodeToString(CreatePostRequest.serializer(), request)

    private fun fields(details: PostDetails, postType: String = "image", mediaIds: List<String> = media): JsonObject =
        json.parseToJsonElement(encode(studioCreateRequest("caption", "en", postType, mediaIds, details))).jsonObject

    @Test
    fun `no details freezes exactly the request from before the details screen`() {
        val before = CreatePostRequest(
            text = "caption",
            postType = "image",
            mediaIds = media,
            language = "en",
            distribution = DistributionRequest(),
        )
        assertThat(encode(studioCreateRequest("caption", "en", "image", media))).isEqualTo(encode(before))
    }

    @Test
    fun `every choice lands under the server's name`() {
        val sent = fields(
            PostDetails(
                title = "  Monsoon walk ",
                hashtags = listOf("rain"),
                mentions = listOf("asha"),
                visibility = "friends",
                allowComments = false,
                allowReuse = false,
                paidPromotion = true,
                isBranded = true,
                aiGenerated = true,
                linkUrl = "https://example.com/a",
                locationName = "Hyderabad",
                locationPlaceId = "place:123",
                coverIndex = 2,
            ),
        )
        assertThat(sent["visibility"]!!.jsonPrimitive.content).isEqualTo("friends")
        assertThat(sent["title"]!!.jsonPrimitive.content).isEqualTo("Monsoon walk")
        assertThat(sent["no_comments"]!!.jsonPrimitive.content).isEqualTo("true")
        assertThat(sent["comment_access"]!!.jsonPrimitive.content).isEqualTo("nobody")
        assertThat(sent["remix_setting"]!!.jsonPrimitive.content).isEqualTo("disallow")
        assertThat(sent["paid_promotion"]!!.jsonPrimitive.content).isEqualTo("true")
        assertThat(sent["is_branded"]!!.jsonPrimitive.content).isEqualTo("true")
        assertThat(sent["ai_generated"]!!.jsonPrimitive.content).isEqualTo("true")
        assertThat(sent["link_url"]!!.jsonPrimitive.content).isEqualTo("https://example.com/a")
        assertThat(sent["location_name"]!!.jsonPrimitive.content).isEqualTo("Hyderabad")
        assertThat(sent["location_place_id"]!!.jsonPrimitive.content).isEqualTo("place:123")
        // The cover is a page of the post: the media id at the index.
        assertThat(sent["cover_media_id"]!!.jsonPrimitive.content).isEqualTo("m-3")
        assertThat(sent["hashtags"].toString()).isEqualTo("[\"rain\"]")
        assertThat(sent["mentions"].toString()).isEqualTo("[\"asha\"]")
    }

    @Test
    fun `comments and reuse on say so in both spellings`() {
        val sent = fields(PostDetails(allowComments = true, allowReuse = true))
        assertThat(sent["no_comments"]!!.jsonPrimitive.content).isEqualTo("false")
        assertThat(sent["comment_access"]!!.jsonPrimitive.content).isEqualTo("everyone")
        assertThat(sent["remix_setting"]!!.jsonPrimitive.content).isEqualTo("allow")
    }

    @Test
    fun `a cover past the pages, or on a text post, sends no cover at all`() {
        assertThat(fields(PostDetails(coverIndex = 7))).doesNotContainKey("cover_media_id")
        assertThat(fields(PostDetails(coverIndex = 0), postType = "text", mediaIds = emptyList()))
            .doesNotContainKey("cover_media_id")
    }

    @Test
    fun `an audience the client does not offer falls back to public`() {
        assertThat(fields(PostDetails(visibility = "unlisted"))["visibility"]!!.jsonPrimitive.content).isEqualTo("public")
    }

    @Test
    fun `a link the server would refuse never reaches the wire`() {
        assertThat(fields(PostDetails(linkUrl = "javascript:alert(1)"))).doesNotContainKey("link_url")
        assertThat(fields(PostDetails(linkUrl = "http://example.com"))).doesNotContainKey("link_url")
    }

    @Test
    fun `a place id without a place name is not sent`() {
        assertThat(fields(PostDetails(locationPlaceId = "place:1"))).doesNotContainKey("location_place_id")
    }

    @Test
    fun `links follow the server's rule`() {
        assertThat(PostLink.normalize(" https://example.com/x?y=1 ")).isEqualTo("https://example.com/x?y=1")
        assertThat(PostLink.normalize("HTTPS://Example.com")).isEqualTo("HTTPS://Example.com")
        assertThat(PostLink.normalize("http://example.com")).isNull()
        assertThat(PostLink.normalize("javascript:alert(1)")).isNull()
        assertThat(PostLink.normalize("data:text/html,hi")).isNull()
        assertThat(PostLink.normalize("https://bank.example@evil.example")).isNull()
        assertThat(PostLink.normalize("https://")).isNull()
        assertThat(PostLink.normalize("https://exa mple.com")).isNull()
        assertThat(PostLink.normalize("https://example.com/" + "a".repeat(PostLink.MAX_LENGTH))).isNull()
        assertThat(PostLink.normalize("")).isNull()
        assertThat(PostLink.problem("")).isNull()
        assertThat(PostLink.problem("example.com")).isEqualTo("Use a full https:// link.")
    }
}
