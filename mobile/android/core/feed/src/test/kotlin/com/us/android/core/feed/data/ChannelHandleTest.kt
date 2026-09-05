package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.model.Channel
import org.junit.Test

/** The handle rules the form applies as the user types, and the Create → Video gate. */
class ChannelHandleTest {

    @Test
    fun `a typed handle is lowercased, stripped of its at-sign and of anything not allowed`() {
        assertThat(ChannelHandle.normalize("@Ada Lovelace!")).isEqualTo("adalovelace")
        assertThat(ChannelHandle.normalize("ada.lovelace_1815")).isEqualTo("ada.lovelace_1815")
        assertThat(ChannelHandle.normalize("ÄDA-lace")).isEqualTo("dalace")
    }

    @Test
    fun `a handle is cut at thirty characters`() {
        val long = "a".repeat(40)

        assertThat(ChannelHandle.normalize(long)).hasLength(ChannelHandle.MAX_LENGTH)
    }

    @Test
    fun `too short and a leading or trailing dot are the two problems`() {
        assertThat(ChannelHandle.problem("ab")).isEqualTo("At least 3 characters")
        assertThat(ChannelHandle.problem(".ada")).isEqualTo("Can't start or end with a dot")
        assertThat(ChannelHandle.problem("ada.")).isEqualTo("Can't start or end with a dot")
        assertThat(ChannelHandle.problem("ada")).isNull()
        assertThat(ChannelHandle.isValid("ada_lovelace")).isTrue()
    }

    @Test
    fun `the suggestion is the username, else the display name squeezed into the rules`() {
        assertThat(ChannelHandle.suggest("Ada_L", "Ada Lovelace")).isEqualTo("ada_l")
        assertThat(ChannelHandle.suggest(null, "Ada Lovelace")).isEqualTo("adalovelace")
        assertThat(ChannelHandle.suggest("", "A")).isEmpty()
    }

    @Test
    fun `a name is required and capped`() {
        assertThat(ChannelName.problem("  ")).isEqualTo("Give your channel a name")
        assertThat(ChannelName.problem("x".repeat(51))).isEqualTo("At most 50 characters")
        assertThat(ChannelName.problem("Ada's Engine")).isNull()
        assertThat(ChannelAbout.problem("")).isNull()
        assertThat(ChannelAbout.problem("x".repeat(501))).isEqualTo("At most 500 characters")
    }

    @Test
    fun `the gate proceeds with a channel, creates first without one, waits while unknown, blocks on a failure`() {
        val channel = Channel(userId = "u", name = "Ada", handle = "ada")

        assertThat(channelGate(ChannelState.Present(channel))).isEqualTo(ChannelGate.Proceed)
        assertThat(channelGate(ChannelState.None)).isEqualTo(ChannelGate.CreateFirst)
        assertThat(channelGate(ChannelState.Unknown)).isEqualTo(ChannelGate.Wait)
        assertThat(channelGate(ChannelState.Failed("offline"))).isEqualTo(ChannelGate.Blocked("offline"))
    }

    @Test
    fun `a create refusal is read by code, never by message`() {
        assertThat(ChannelRepository.createError(AppError.Unknown(code = "HANDLE_TAKEN", statusCode = 409)))
            .isEqualTo(ChannelCreateError.HandleTaken)
        assertThat(ChannelRepository.createError(AppError.Unknown(code = "CHANNEL_EXISTS", statusCode = 409)))
            .isEqualTo(ChannelCreateError.ChannelExists)
        assertThat(ChannelRepository.createError(AppError.Unknown(code = "INVALID_HANDLE", statusCode = 400)))
            .isInstanceOf(ChannelCreateError.InvalidHandle::class.java)
        assertThat(ChannelRepository.createError(AppError.Unknown(code = "INVALID_NAME", statusCode = 400)))
            .isInstanceOf(ChannelCreateError.InvalidName::class.java)
        assertThat(ChannelRepository.createError(AppError.Unknown(code = "INVALID_ABOUT", statusCode = 400)))
            .isInstanceOf(ChannelCreateError.InvalidAbout::class.java)
        assertThat(ChannelRepository.createError(AppError.NoNetwork()))
            .isEqualTo(ChannelCreateError.Other("You're offline. Check your connection and try again."))
    }

    @Test
    fun `only a 403 CHANNEL_REQUIRED means make a channel first`() {
        assertThat(ChannelRepository.requiresChannel(AppError.Forbidden(code = "CHANNEL_REQUIRED"))).isTrue()
        assertThat(ChannelRepository.requiresChannel(AppError.Forbidden(code = "BLOCKED"))).isFalse()
        assertThat(ChannelRepository.requiresChannel(AppError.NotFound())).isFalse()
    }
}
