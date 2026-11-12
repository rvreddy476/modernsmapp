package com.us.android.feature.echo

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.echo.data.CreateNoteRequest
import com.us.android.feature.echo.data.CreateThreadRequest
import com.us.android.feature.echo.data.EchoErrorCodes
import com.us.android.feature.echo.data.EligibilityDto
import com.us.android.feature.echo.data.EligibilityReason
import com.us.android.feature.echo.data.NoteDto
import com.us.android.feature.echo.data.NoteListDto
import com.us.android.feature.echo.data.ReplyPolicy
import com.us.android.feature.echo.data.ThreadEntryRequest
import com.us.android.feature.echo.data.VerificationMark
import com.us.android.feature.echo.data.toEligibility
import com.us.android.feature.echo.data.toNote
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import org.junit.Test

/**
 * The golden fixtures, copied byte for byte from post-service
 * `testdata/contracts/notes/`, read through the app's own Json settings.
 */
class EchoContractTest {

    /** The app's Json (:core:network NetworkModule.provideJson). */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private fun fixture(name: String): String =
        requireNotNull(
            javaClass.classLoader?.getResource("contracts/notes/$name.json")
        ) { "missing fixture $name" }.readText()

    @Test
    fun `a note reads with its author mark, media, quote and counts`() {
        val note = json.decodeFromString<ApiEnvelope<NoteDto>>(fixture("note")).data!!.toNote()
        assertThat(note.id).isEqualTo("f1111111-1111-4111-8111-111111111111")
        assertThat(note.text).isEqualTo("Rain all week in #Bengaluru")
        assertThat(note.conversationId).isEqualTo(note.id)
        assertThat(note.replyToId).isNull()
        assertThat(note.edited).isFalse()
        assertThat(note.replyPolicy).isEqualTo(ReplyPolicy.EVERYONE)
        assertThat(note.replyCount).isEqualTo(2)
        assertThat(note.likeCount).isEqualTo(4)
        assertThat(note.repostCount).isEqualTo(1)
        assertThat(note.liked).isTrue()
        assertThat(note.reposted).isFalse()
        assertThat(note.viewerCanReply).isTrue()
        assertThat(note.media.map { it.mediaId }).containsExactly("e1111111-1111-4111-8111-111111111111")
        assertThat(note.author!!.name).isEqualTo("Alice Rao")
        assertThat(note.author!!.handle).isEqualTo("@alice")
        assertThat(note.author!!.mark).isEqualTo(VerificationMark.PERSON)

        val quoted = note.quoted!!
        assertThat(note.quoteOfId).isEqualTo(quoted.id)
        assertThat(quoted.text).isEqualTo("Monsoon is here.")
        assertThat(quoted.quoted).isNull()
        assertThat(quoted.media).isEmpty()
    }

    @Test
    fun `a list reads its replies and an empty cursor is the last page`() {
        val list = json.decodeFromString<ApiEnvelope<NoteListDto>>(fixture("note_list")).data!!
        val notes = list.items!!.map { it.toNote() }
        assertThat(notes.map { it.id }).containsExactly(
            "f2222222-2222-4222-8222-222222222222",
            "f3333333-3333-4333-8333-333333333333",
        ).inOrder()
        assertThat(notes.all { it.replyToId == "f1111111-1111-4111-8111-111111111111" }).isTrue()
        assertThat(notes[0].author!!.mark).isEqualTo(VerificationMark.NONE)
        assertThat(list.nextCursor).isEmpty()
    }

    @Test
    fun `eligibility reads premium required with replies still open`() {
        val eligibility = json.decodeFromString<ApiEnvelope<EligibilityDto>>(
            fixture("eligibility")
        ).data!!.toEligibility()
        assertThat(eligibility.canPost).isFalse()
        assertThat(eligibility.canReply).isTrue()
        assertThat(eligibility.reason).isEqualTo(EligibilityReason.PREMIUM_REQUIRED)
    }

    @Test
    fun `the premium refusal carries the contract code`() {
        val envelope = json.decodeFromString<ApiEnvelope<JsonObject>>(fixture("error_premium_required"))
        assertThat(envelope.error!!.code).isEqualTo(EchoErrorCodes.PREMIUM_REQUIRED)
        assertThat(envelope.data).isNull()
    }

    @Test
    fun `requests send the contract's field names and leave out what is not set`() {
        val wire = Json { explicitNulls = false }
        val reply = wire.encodeToString(
            CreateNoteRequest.serializer(),
            CreateNoteRequest(text = "hi", replyToId = "n1")
        )
        assertThat(reply).isEqualTo("""{"text":"hi","reply_to_id":"n1"}""")
        val quote = wire.encodeToString(
            CreateNoteRequest.serializer(),
            CreateNoteRequest(text = "q", mediaIds = listOf("m1"), quoteOfId = "n2", replyPolicy = "following"),
        )
        assertThat(quote).isEqualTo("""{"text":"q","media_ids":["m1"],"quote_of_id":"n2","reply_policy":"following"}""")
        val thread = wire.encodeToString(
            CreateThreadRequest.serializer(),
            CreateThreadRequest(listOf(ThreadEntryRequest("a"), ThreadEntryRequest("b", listOf("m"))), "mentioned"),
        )
        assertThat(
            thread
        ).isEqualTo("""{"entries":[{"text":"a"},{"text":"b","media_ids":["m"]}],"reply_policy":"mentioned"}""")
    }

    @Test
    fun `the author marks follow verification, never Premium`() {
        assertThat(VerificationMark.of(false, "person")).isEqualTo(VerificationMark.NONE)
        assertThat(VerificationMark.of(true, "person")).isEqualTo(VerificationMark.PERSON)
        assertThat(VerificationMark.of(true, "")).isEqualTo(VerificationMark.PERSON)
        assertThat(VerificationMark.of(true, "organisation")).isEqualTo(VerificationMark.OFFICIAL)
        assertThat(VerificationMark.of(true, "government")).isEqualTo(VerificationMark.GOVERNMENT)
    }
}
