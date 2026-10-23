package com.us.android.feature.post.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.creator.model.PostDetails
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import org.junit.Test
import java.io.File

/**
 * The composer drafts' wire (2026-10-18). post-service decodes a draft's
 * payload with `DisallowUnknownFields`, so ONE key it does not declare and
 * every save is a 400 — the defect that once broke every scheduled reel.
 * The payload keys are therefore checked against the Go struct's own json
 * tags, read from the service's source, not against a list kept here.
 */
class PostDraftsContractTest {

    /** Mirrors NetworkModule.provideJson(): no defaults, no explicit nulls. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private val goSource = File("../../../../Architecture/services/post-service/internal/service/post_drafts.go")

    /** The json names `PostDraftPayload` declares. */
    private fun serverPayloadKeys(): Set<String> {
        val source = goSource.readText()
        val body = source.substringAfter("type PostDraftPayload struct {").substringBefore("\n}")
        return Regex("json:\"([a-z_]+)").findAll(body).map { it.groupValues[1] }.toSet()
    }

    private val everything = DraftContent(
        text = "rain #monsoon",
        language = "en",
        mediaIds = listOf("m-1", "m-2"),
        altTexts = mapOf("m-1" to "a wet street", "m-9" to "not this post's"),
        details = PostDetails(
            title = "Monsoon",
            hashtags = listOf("monsoon"),
            mentions = listOf("asha"),
            visibility = "friends",
            allowComments = false,
            allowReuse = false,
            paidPromotion = true,
            isBranded = true,
            aiGenerated = true,
            linkUrl = "https://example.com",
            locationName = "Goa",
            locationPlaceId = "place:1",
            coverIndex = 1,
            // The Action button and the members-only level (2026-10-23) round-trip too.
            actionLabel = "apply_now",
            actionUrl = "https://epfindia.gov.in/apply",
            tierRequiredId = "7b1d3c2e-0000-4000-8000-000000000001",
        ),
    )

    @Test
    fun `every key a draft sends is one post-service declares`() {
        val sent = json.parseToJsonElement(
            json.encodeToString(PostDraftRequestDto.serializer(), PostDraftRequestDto(payload = draftPayload(everything))),
        ).jsonObject
        assertThat(sent.keys).containsExactly("post_type", "payload")
        val payloadKeys = sent.getValue("payload").jsonObject.keys
        assertThat(serverPayloadKeys()).isNotEmpty()
        assertThat(payloadKeys - serverPayloadKeys()).isEmpty()
    }

    @Test
    fun `a draft reads back as it was saved`() {
        val dto = PostDraftDto(id = "d-1", postType = "post", payload = draftPayload(everything), status = "draft")
        val back = json.decodeFromString(PostDraftDto.serializer(), json.encodeToString(PostDraftDto.serializer(), dto)).toDraft()

        assertThat(back.text).isEqualTo(everything.text)
        assertThat(back.mediaIds).isEqualTo(everything.mediaIds)
        // Only this post's photos keep their descriptions.
        assertThat(back.altTexts).containsExactly("m-1", "a wet street")
        assertThat(back.details).isEqualTo(everything.details)
    }

    @Test
    fun `an older draft with only the old spellings still reads`() {
        val raw = """{"id":"d","post_type":"post","status":"draft",
            |"payload":{"text":"hi","no_comments":true,"cover_media_id":"m-2","media_ids":["m-1","m-2"]}}""".trimMargin()
        val draft = json.decodeFromString(PostDraftDto.serializer(), raw).toDraft()
        assertThat(draft.details.allowComments).isFalse()
        assertThat(draft.details.coverIndex).isEqualTo(1)
        assertThat(draft.language).isEqualTo("en")
    }

    @Test
    fun `a bare text draft sends text and nothing it was not given`() {
        val payload = json.parseToJsonElement(
            json.encodeToString(PostDraftPayloadDto.serializer(), draftPayload(DraftContent(text = "hi", language = "en", mediaIds = emptyList()))),
        ).jsonObject
        assertThat(payload.keys).containsExactly("text", "visibility", "content_type", "language", "distribution", "post_type")
    }
}
