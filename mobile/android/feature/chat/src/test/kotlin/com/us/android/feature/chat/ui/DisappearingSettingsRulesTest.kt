package com.us.android.feature.chat.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import org.junit.Test

/** What the chat settings page says when a disappearing-timer change is refused. */
class DisappearingSettingsRulesTest {

    @Test
    fun `a plain group member is told who may change it`() {
        assertThat(disappearingRefusal(AppError.Forbidden(code = "FORBIDDEN")))
            .isEqualTo("Only the group's owner or admins can change disappearing messages.")
    }

    @Test
    fun `any other refusal is a plain failure to save`() {
        assertThat(disappearingRefusal(AppError.NotFound())).startsWith("Couldn't change disappearing messages")
        assertThat(disappearingRefusal(AppError.NoNetwork())).startsWith("Couldn't change disappearing messages")
    }
}
