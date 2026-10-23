package com.us.android.feature.post

import com.google.common.truth.Truth.assertThat

import com.us.android.core.model.ActionDraft
import com.us.android.core.network.di.NetworkModule
import com.us.android.feature.post.composer.ComposerReducer
import com.us.android.feature.post.composer.ComposerUiState
import com.us.android.feature.post.composer.PostBlockedReason
import com.us.android.feature.post.createhub.PendingReelPublish
import com.us.android.feature.post.createhub.ReelPublishPipeline
import com.us.android.feature.post.createhub.ReelPublishViewModel
import com.us.android.feature.post.createhub.VideoProbe
import com.us.android.feature.post.data.DraftContent
import com.us.android.feature.post.data.PostDraftDto
import com.us.android.feature.post.data.draftPayload
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.POST_TYPE_IMAGE
import com.us.android.feature.post.data.toDraft
import com.us.android.feature.post.studio.PostDetailsUi
import com.us.android.feature.post.studio.studioCreateRequest
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import org.junit.Test

/**
 * The Action button in what `:feature:post` publishes (2026-10-23): the
 * text composer, the reel and long-video form and the photo studio send
 * `action` ONLY when there is a button, trimmed; the studio and its server
 * drafts carry the members-only level and the button and read both back.
 */
class ActionButtonPostTest {

    private val json = NetworkModule.provideJson()
    private val button = ActionDraft("apply_now", " https://epfindia.gov.in/apply ")

    private fun CreatePostRequest.wire(): JsonObject =
        json.parseToJsonElement(json.encodeToString(CreatePostRequest.serializer(), this)).jsonObject

    @Test
    fun `the text composer sends the button with the create, and nothing without one`() {
        val plain = ComposerUiState(text = "hello")
        assertThat(ComposerReducer.buildRequest(plain).wire().keys).doesNotContain("action")
        val withButton = ComposerReducer.buildRequest(plain.copy(action = button)).wire()
        assertThat(withButton["action"]!!.jsonObject.toString())
            .isEqualTo("""{"label":"apply_now","url":"https://epfindia.gov.in/apply"}""")
    }

    @Test
    fun `a link the server would refuse blocks Post in the composer, with its reason`() {
        val state = ComposerUiState(text = "hello", action = ActionDraft("apply_now", "https://bit.ly/x"))
        assertThat(state.blockedReason).isEqualTo(PostBlockedReason.ActionInvalid)
        assertThat(state.copy(action = ActionDraft("apply_now", "https://wa.me/919876543210")).canPost).isTrue()
    }

    @Test
    fun `the reel form records the trimmed button and the worker sends it`() {
        val form = ReelPublishViewModel.ReelUiState(videoUri = "content://v", action = button)
        val pending = ReelPublishViewModel.buildPending(form, "key-1", null)
        assertThat(pending.actionLabel).isEqualTo("apply_now")
        assertThat(pending.actionUrl).isEqualTo("https://epfindia.gov.in/apply")
        val sent = ReelPublishPipeline.buildRequest(pending, "video-1", null, language = "en").wire()
        assertThat(sent["action"]!!.jsonObject.toString())
            .isEqualTo("""{"label":"apply_now","url":"https://epfindia.gov.in/apply"}""")

        val none = ReelPublishViewModel.buildPending(form.copy(action = null), "key-1", null)
        assertThat(none.actionLabel).isNull()
        assertThat(ReelPublishPipeline.buildRequest(none, "video-1", null, language = "en").wire().keys).doesNotContain("action")
        // A record written before the button existed loads with none.
        val old = json.decodeFromString(PendingReelPublish.serializer(), """{"creationKey":"k","videoUri":"content://v"}""")
        assertThat(old.actionLabel).isNull()
    }

    @Test
    fun `a group invite blocks Post on the reel form`() {
        val form = ReelPublishViewModel.ReelUiState(
            videoUri = "content://v",
            probe = VideoProbe(durationMs = 20_000, sizeBytes = 2_000_000),
        )
        assertThat(form.canPost).isTrue()
        assertThat(form.copy(action = ActionDraft("sign_up", "https://chat.whatsapp.com/AbC")).canPost).isFalse()
        assertThat(form.copy(action = ActionDraft("sign_up", "https://t.me/channel")).canPost).isTrue()
    }

    @Test
    fun `the photo studio sends the button and the members-only level it was given`() {
        val details = PostDetailsUi(membersLevelId = "tier-1").withAction(button)
        val request = studioCreateRequest("hi", "en", POST_TYPE_IMAGE, listOf("m-1"), details.toDetails("hi", 1)).wire()
        assertThat(request["action"]!!.jsonObject.toString())
            .isEqualTo("""{"label":"apply_now","url":"https://epfindia.gov.in/apply"}""")
        assertThat(request["tier_required_id"].toString()).isEqualTo("\"tier-1\"")

        val bare = studioCreateRequest("hi", "en", POST_TYPE_IMAGE, listOf("m-1"), PostDetailsUi().toDetails("hi", 1)).wire()
        assertThat(bare.keys).containsNoneOf("action", "tier_required_id")
        // Taking the button off is no button.
        assertThat(details.withAction(null).action).isNull()
        assertThat(details.withAction(null).toDetails("hi", 1).actionLabel).isNull()
    }

    @Test
    fun `the details form is not valid with a shortener`() {
        assertThat(PostDetailsUi().withAction(ActionDraft("apply_now", "https://tinyurl.com/x")).valid).isFalse()
        assertThat(PostDetailsUi().withAction(ActionDraft("apply_now", "https://example.com/x")).valid).isTrue()
    }

    @Test
    fun `a server draft round-trips the members-only level and the button`() {
        val details = PostDetailsUi(membersLevelId = "tier-1").withAction(button).toDetails("hi", 1)
        val content = DraftContent(text = "hi", language = "en", mediaIds = listOf("m-1"), details = details)
        val payload = draftPayload(content)
        assertThat(payload.tierRequiredId).isEqualTo("tier-1")
        assertThat(payload.action.toString()).isEqualTo("""{"label":"apply_now","url":"https://epfindia.gov.in/apply"}""")

        val dto = PostDraftDto(id = "d-1", postType = "post", payload = payload, status = "draft")
        val back = json.decodeFromString(PostDraftDto.serializer(), json.encodeToString(PostDraftDto.serializer(), dto)).toDraft()
        assertThat(back.details.tierRequiredId).isEqualTo("tier-1")
        assertThat(back.details.actionLabel).isEqualTo("apply_now")
        assertThat(back.details.actionUrl).isEqualTo("https://epfindia.gov.in/apply")
        // And into the form as the editor holds it.
        val form = PostDetailsUi.from(back.details)
        assertThat(form.membersLevelId).isEqualTo("tier-1")
        assertThat(form.action).isEqualTo(ActionDraft("apply_now", "https://epfindia.gov.in/apply"))

        // A draft saved with neither reads back with neither, and a stray action shape is no button.
        val raw = """{"id":"d","post_type":"post","status":"draft","payload":{"text":"hi","action":"oops","tier_required_id":""}}"""
        val stray = json.decodeFromString(PostDraftDto.serializer(), raw).toDraft()
        assertThat(stray.details.actionLabel).isNull()
        assertThat(stray.details.tierRequiredId).isNull()
    }
}
