package com.us.android.core.feed.data.action

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.feed.data.ChannelDto
import com.us.android.core.feed.data.PostEditDetailDto
import com.us.android.core.feed.data.PostEditPatch
import com.us.android.core.feed.data.UpdateChannelRequest
import com.us.android.core.feed.data.applyTo
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.feed.data.toDomain
import com.us.android.core.feed.data.withSavedAction
import com.us.android.core.feed.ui.edit.PostEditUiState
import com.us.android.core.feed.ui.edit.actionPatch
import com.us.android.core.model.ActionButtons
import com.us.android.core.model.ActionDraft
import com.us.android.core.model.ActionErrorCode
import com.us.android.core.model.ActionPatch
import com.us.android.core.model.ActionStatus
import com.us.android.core.network.di.NetworkModule
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Test

/**
 * The Action button on the wire (contract 23 Oct 2026): rows read it
 * tolerantly and never hand a viewer a raw url to open; the PATCH leaves the
 * key off when nothing moved, sends JSON null to remove and `{label, url}` to
 * set; the create sends it only when there is one; a refusal reads as its
 * sentence, ACTION_NOT_ELIGIBLE naming what is missing.
 */
class ActionWireTest {

    private val json = NetworkModule.provideJson()

    private fun row(action: String?): FeedItemDto {
        val tail = if (action == null) "" else ""","action":$action"""
        return json.decodeFromString(FeedItemDto.serializer(), """{"id":"p1","author_id":"u1"$tail}""")
    }

    @Test
    fun `absent, null, empty, an array or a shape without an id or label is no button`() {
        listOf(null, "null", "\"\"", "[]", "{}", """{"id":"a1"}""", """{"label":"apply_now"}""", """{"id":7,"label":"apply_now"}""")
            .forEach { assertThat(row(it).toDomain().action).isNull() }
    }

    @Test
    fun `a viewer's row reads as a live button that links to our redirect only`() {
        val item = row("""{"id":"a1","label":"apply_now","label_text":"Apply now","domain":"epfindia.gov.in","href":"/l/a1"}""")
            .toDomain()
        val action = item.action!!
        assertThat(action.href).isEqualTo("/l/a1")
        assertThat(action.status).isEqualTo(ActionStatus.ACTIVE)
        assertThat(action.url).isNull()
        assertThat(action.isAuthorCopy).isFalse()
        assertThat(action.pillText).isEqualTo("Apply now · epfindia.gov.in")
        // A stray raw url on a viewer's row is never the link, and never reaches the viewer.
        val leaked = row("""{"id":"a1","label":"apply_now","href":"https://evil.example/x","url":"https://evil.example/x"}""")
            .toDomain().action!!
        assertThat(leaked.href).isEqualTo("/l/a1")
        assertThat(ActionButtons.visible(leaked.copy(status = ActionStatus.ACTIVE), isAuthor = false)!!.url).isNull()
    }

    @Test
    fun `the author's copy carries its url and a status that is not live`() {
        val item = row(
            """{"id":"a1","label":"book_now","label_text":"Book now","domain":"x.in","href":"/l/a1",""" +
                """"status":"under_review","url":"https://x.in/book"}""",
        ).toDomain()
        val action = item.action!!
        assertThat(action.status).isEqualTo(ActionStatus.UNDER_REVIEW)
        assertThat(action.statusReason).isNull()
        assertThat(action.url).isEqualTo("https://x.in/book")
        val removed = row(
            """{"id":"a1","label":"book_now","href":"/l/a1","status":"blocked","status_reason":"moderator","url":"https://x.in"}""",
        ).toDomain().action!!
        assertThat(removed.statusReason).isEqualTo("moderator")
        assertThat(ActionButtons.statusNote(removed)!!.title).isEqualTo("Removed by our team")
        assertThat(action.isAuthorCopy).isTrue()
        assertThat(ActionButtons.visible(action, isAuthor = false)).isNull()
        assertThat(ActionButtons.visible(action, isAuthor = true)).isEqualTo(action)
    }

    @Test
    fun `the edit PATCH omits an unchanged button, removes with null and sets label and url`() {
        val saved = ActionDraft("apply_now", "https://x.in/apply")
        val base = PostEditUiState(actionBase = saved, action = saved)
        fun body(state: PostEditUiState) = json.encodeToString(PostEditPatch.serializer(), actionPatch(state))

        assertThat(body(base)).isEqualTo("{}")
        assertThat(actionPatch(base).isEmpty).isTrue()
        assertThat(base.dirty).isFalse()
        assertThat(body(base.copy(action = null))).isEqualTo("""{"action":null}""")
        assertThat(base.copy(action = null).dirty).isTrue()
        assertThat(body(base.copy(action = saved.copy(url = " https://x.in/new "))))
            .isEqualTo("""{"action":{"label":"apply_now","url":"https://x.in/new"}}""")
        assertThat(body(PostEditUiState(actionBase = null, action = saved)))
            .isEqualTo("""{"action":{"label":"apply_now","url":"https://x.in/apply"}}""")
        assertThat(body(PostEditUiState(actionBase = null, action = null))).isEqualTo("{}")
        // Merged with another field, both ride one request; a later save's action wins.
        val merged = PostEditPatch(title = "T").then(actionPatch(base.copy(action = null)))
        assertThat(json.encodeToString(PostEditPatch.serializer(), merged)).isEqualTo("""{"title":"T","action":null}""")
    }

    @Test
    fun `the patch builder maps each case to its wire value`() {
        assertThat(ActionPatch.Unchanged.toJson()).isNull()
        assertThat(ActionPatch.Remove.toJson()).isEqualTo(JsonNull)
        assertThat(ActionPatch.Set("shop_now", "https://x.in").toJson()).isEqualTo(
            buildJsonObject {
                put("label", "shop_now")
                put("url", "https://x.in")
            },
        )
    }

    @Test
    fun `the profile PATCH carries the same three shapes`() {
        fun body(patch: ActionPatch) =
            json.encodeToString(UpdateChannelRequest.serializer(), UpdateChannelRequest(action = patch.toJson()))
        assertThat(body(ActionPatch.Unchanged)).isEqualTo("{}")
        assertThat(body(ActionPatch.Remove)).isEqualTo("""{"action":null}""")
        assertThat(body(ActionPatch.Set("book_now", "https://x.in"))).isEqualTo("""{"action":{"label":"book_now","url":"https://x.in"}}""")
        // The channel read carries the button, null when there is none.
        val channel = json.decodeFromString(
            ChannelDto.serializer(),
            """{"user_id":"u1","handle":"ada","action":{"id":"a9","label":"book_now","domain":"x.in","href":"/l/a9"}}""",
        )
        assertThat(channel.action.toPostAction()!!.id).isEqualTo("a9")
        assertThat(json.decodeFromString(ChannelDto.serializer(), """{"user_id":"u1","action":null}""").action.toPostAction()).isNull()
    }

    @Test
    fun `the create carries the trimmed button only when there is one`() {
        assertThat((null as ActionDraft?).toWriteDto()).isNull()
        assertThat(ActionDraft("apply_now", " https://x.in ").toWriteDto()).isEqualTo(ActionWriteDto("apply_now", "https://x.in"))
    }

    @Test
    fun `a saved draft's button reads back, anything else is none`() {
        assertThat(buildJsonObject { put("label", "apply_now"); put("url", "https://x.in") }.toActionDraft())
            .isEqualTo(ActionDraft("apply_now", "https://x.in"))
        assertThat(JsonNull.toActionDraft()).isNull()
        assertThat(JsonPrimitive("x").toActionDraft()).isNull()
        assertThat(buildJsonObject { put("url", "https://x.in") }.toActionDraft()).isNull()
    }

    @Test
    fun `a landed save layers the server's button over the rows, or takes it off`() {
        val item = row("""{"id":"a1","label":"apply_now","href":"/l/a1","url":"https://x.in/old"}""").toDomain()
        val answer = json.decodeFromString(
            PostEditDetailDto.serializer(),
            """{"id":"p1","action":{"id":"a2","label":"book_now","domain":"y.in","href":"/l/a2","url":"https://y.in"}}""",
        )
        val changed = PostEditPatch(action = ActionPatch.Set("book_now", "https://y.in").toJson()).withSavedAction(answer)
        assertThat(mapOf("p1" to changed).applyTo(item).action!!.id).isEqualTo("a2")

        val removed = PostEditPatch(action = JsonNull).withSavedAction(PostEditDetailDto(id = "p1"))
        assertThat(mapOf("p1" to removed).applyTo(item).action).isNull()

        // A save that did not touch the button leaves it as it was.
        val other = PostEditPatch(title = "T").withSavedAction(PostEditDetailDto(id = "p1"))
        assertThat(other.action).isNull()
        assertThat(mapOf("p1" to other).applyTo(item).action).isEqualTo(item.action)
    }

    @Test
    fun `a refusal of the button reads as its sentence, wherever the mapper put the code`() {
        assertThat(AppError.Unknown(code = "ACTION_LINK_SHORTENER", statusCode = 400).actionProblem()!!.code)
            .isEqualTo(ActionErrorCode.ACTION_LINK_SHORTENER)
        assertThat(AppError.Server(statusCode = 503, code = "ACTION_LINK_CHECK_UNAVAILABLE").actionProblem()!!.message)
            .isEqualTo("We couldn't check that link right now. Try again in a few minutes.")
        val notEligible = AppError.Forbidden(
            code = "ACTION_NOT_ELIGIBLE",
            details = mapOf("missing" to """["account_age","email_verified","no_active_strikes"]"""),
        ).actionProblem()!!
        assertThat(notEligible.missing).containsExactly("account_age", "email_verified", "no_active_strikes").inOrder()
        assertThat(notEligible.message).isEqualTo(
            "You can't add an action button yet. Your account must be at least 7 days old. " +
                "Verify your email address. You can't have an active strike on your account.",
        )
        // An account too new is told from when (details.eligible_at).
        val tooNew = AppError.Forbidden(
            code = "ACTION_NOT_ELIGIBLE",
            details = mapOf("missing" to """["account_age"]""", "eligible_at" to "2026-10-30T09:00:00Z"),
        ).actionProblem()!!
        assertThat(tooNew.eligibleAt).isEqualTo("2026-10-30T09:00:00Z")
        assertThat(tooNew.message).startsWith("You can't add an action button yet. You can add an action button from ")
        // No details: the general sentence.
        assertThat(AppError.Forbidden(code = "ACTION_NOT_ELIGIBLE").actionProblem()!!.missing).isEmpty()
        // Not the button's: null, so the host says its own words.
        assertThat(AppError.Forbidden(code = "CHANNEL_REQUIRED").actionProblem()).isNull()
        assertThat(AppError.NoNetwork().actionProblem()).isNull()
    }
}
