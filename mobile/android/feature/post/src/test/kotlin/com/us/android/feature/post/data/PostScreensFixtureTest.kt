package com.us.android.feature.post.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.creator.model.PostDetails
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.PostDto
import com.us.android.feature.post.studio.studioCreateRequest
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Test
import java.io.File

/**
 * post-service's post-screens goldens (lane BP, 795058cf), copied byte for
 * byte into `src/test/resources/contracts/posts` and parsed with the
 * platform's own `Json` — so a drift on either side turns this red.
 */
class PostScreensFixtureTest {

    private val app = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }
    private val dir = File("src/test/resources/contracts/posts")
    private val source = File("../../../../Architecture/services/post-service/internal/http/testdata/contracts/posts")
    private fun read(name: String) = File(dir, name).readText()
    private fun obj(name: String): JsonObject = app.parseToJsonElement(read(name)).jsonObject

    @Test
    fun `the copies are byte-identical to post-service's goldens`() {
        val names = dir.listFiles().orEmpty().map { it.name }.sorted()
        assertThat(names).containsExactly(
            "create_request.json", "create_response.json", "draft_request.json", "draft_response.json",
            "drafts_list.json", "patch_request.json", "post_detail.json",
        )
        names.forEach { name -> assertThat(File(dir, name).readBytes()).isEqualTo(File(source, name).readBytes()) }
    }

    /** Every details key the Studio sends is one the golden create request carries, with the same value. */
    @Test
    fun `the studio's request matches the golden create request on every details field`() {
        val golden = obj("create_request.json")
        // The golden sends `distribution: null`, which our request never does; read only its media ids.
        val mediaIds = golden.getValue("media_ids").jsonArray.map { it.jsonPrimitive.content }
        val ours = app.parseToJsonElement(
            app.encodeToString(
                CreatePostRequest.serializer(),
                studioCreateRequest(
                    text = "Sunday market haul #food @call.b",
                    language = "en",
                    postType = "image",
                    mediaIds = mediaIds,
                    details = PostDetails(
                        title = "Sunday market",
                        hashtags = listOf("food", "market"),
                        mentions = listOf("call.b"),
                        visibility = "friends",
                        allowReuse = false,
                        paidPromotion = false,
                        isBranded = true,
                        aiGenerated = true,
                        linkUrl = "https://example.com/sunday-market",
                        locationName = "Banjara Hills, Hyderabad",
                        locationPlaceId = "ChIJLbZ-NFv9DDkRQJY4FbcFcgM",
                        coverIndex = 1,
                    ),
                ),
            ),
        ).jsonObject
        val keys = listOf(
            "visibility", "title", "remix_setting", "cover_media_id", "hashtags", "mentions", "paid_promotion",
            "is_branded", "ai_generated", "link_url", "location_name", "location_place_id", "post_type", "media_ids",
        )
        keys.forEach { key -> assertThat(ours[key]).isEqualTo(golden[key]) }
        // Nothing we send is a key the server's request does not have.
        assertThat(ours.keys - golden.keys).isEmpty()
    }

    @Test
    fun `the golden draft reads into the details it holds`() {
        listOf(
            app.decodeFromString(PostDraftDto.serializer(), read("draft_response.json")),
            app.decodeFromString(ListSerializer(PostDraftDto.serializer()), read("drafts_list.json")).single(),
        ).forEach { dto ->
            val draft = dto.toDraft()
            assertThat(draft.status).isEqualTo("draft")
            assertThat(draft.mediaIds).hasSize(3)
            assertThat(draft.altTexts).hasSize(2)
            with(draft.details) {
                assertThat(title).isEqualTo("Sunday market")
                assertThat(visibility).isEqualTo("friends")
                assertThat(coverIndex).isEqualTo(1)
                assertThat(allowReuse).isFalse()
                assertThat(isBranded).isTrue()
                assertThat(aiGenerated).isTrue()
                assertThat(linkUrl).isEqualTo("https://example.com/sunday-market")
                assertThat(locationPlaceId).isEqualTo("ChIJLbZ-NFv9DDkRQJY4FbcFcgM")
                // "followers" is still comments on, for the form's single switch.
                assertThat(allowComments).isTrue()
            }
        }
    }

    /** The golden draft request's payload is the shape our payload type decodes, key for key. */
    @Test
    fun `the golden draft request decodes and our payload uses only its keys and post-service's`() {
        val golden = app.decodeFromString(PostDraftRequestDto.serializer(), read("draft_request.json"))
        assertThat(golden.postType).isEqualTo(DRAFT_KIND_POST)
        assertThat(golden.payload.coverIndex).isEqualTo(1)
        val goldenPayloadKeys = obj("draft_request.json").getValue("payload").jsonObject.keys
        val ours = app.parseToJsonElement(
            app.encodeToString(PostDraftPayloadDto.serializer(), draftPayload(golden.toContent())),
        ).jsonObject
        // Every key of ours the golden also uses carries the golden's value.
        // comment_access aside: the form has one switch, so the golden's "followers" reads back as on ("everyone").
        (ours.keys intersect goldenPayloadKeys - "comment_access").forEach { key ->
            assertThat(ours[key]).isEqualTo(obj("draft_request.json").getValue("payload").jsonObject[key])
        }
    }

    @Test
    fun `a created post and a post read decode with the platform json`() {
        listOf("create_response.json", "post_detail.json").forEach { name ->
            val post = app.decodeFromString(PostDto.serializer(), read(name))
            assertThat(post.id).isEqualTo("c1111111-1111-4111-8111-111111111111")
        }
    }

    private fun PostDraftRequestDto.toContent(): DraftContent {
        val draft = PostDraftDto(id = "x", postType = postType, payload = payload, status = "draft").toDraft()
        return DraftContent(draft.text, draft.language, draft.mediaIds, draft.altTexts, draft.details)
    }
}
