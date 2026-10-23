package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.engagement.data.CommentsUiState
import org.junit.Test

/**
 * A members-only post's thread, to a non-member (paid memberships,
 * 2026-10-23): post-service answers 403 MEMBERS_ONLY (http/post_edit.go), and
 * the sheet must read that as "for members" with Join, not as a failure.
 */
class MembersOnlyCommentsTest {

    @Test
    fun `a 403 MEMBERS_ONLY on the read or the post is the members-only thread`() {
        val refused = AppError.Forbidden(code = "MEMBERS_ONLY", details = mapOf("creator_id" to "c1"))
        assertThat(CommentsUiState(refreshError = refused).membersOnly).isTrue()
        assertThat(CommentsUiState(submitError = refused).membersOnly).isTrue()
    }

    @Test
    fun `any other failure is not`() {
        assertThat(CommentsUiState().membersOnly).isFalse()
        assertThat(CommentsUiState(refreshError = AppError.Forbidden(code = "FORBIDDEN")).membersOnly).isFalse()
        assertThat(CommentsUiState(refreshError = AppError.NoNetwork()).membersOnly).isFalse()
        assertThat(
            CommentsUiState(refreshError = AppError.Unknown(code = "MEMBERS_ONLY", statusCode = 409)).membersOnly
        ).isFalse()
    }
}
