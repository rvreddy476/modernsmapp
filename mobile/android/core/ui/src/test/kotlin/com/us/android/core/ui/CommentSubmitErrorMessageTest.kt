package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import org.junit.Test

/**
 * The comment composer's failure text — specifically the friends-only refusal
 * `COMMENTS_RESTRICTED` (`403 {"error":{"code":"COMMENTS_RESTRICTED"}}`), which
 * gets its own honest wording rather than the generic "try again" every other
 * submit failure uses.
 */
class CommentSubmitErrorMessageTest {

    @Test
    fun `COMMENTS_RESTRICTED surfaces as a friends-only refusal`() {
        val error = AppError.Forbidden(code = "COMMENTS_RESTRICTED")

        assertThat(commentSubmitErrorMessage(error)).isEqualTo("Only friends can comment on this post")
    }

    /** A different 403 code must not be mistaken for the audience refusal. */
    @Test
    fun `an unrelated forbidden code keeps the generic retry message`() {
        val error = AppError.Forbidden(code = "SOME_OTHER_CODE")

        assertThat(commentSubmitErrorMessage(error))
            .isEqualTo("Your comment wasn't posted. Tap send to try again.")
    }

    @Test
    fun `a transient failure keeps the generic retry message`() {
        val error = AppError.NoNetwork()

        assertThat(commentSubmitErrorMessage(error))
            .isEqualTo("Your comment wasn't posted. Tap send to try again.")
    }
}
