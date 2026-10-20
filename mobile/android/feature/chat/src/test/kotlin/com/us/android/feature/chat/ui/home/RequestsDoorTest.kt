package com.us.android.feature.chat.ui.home

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The requests door shows hidden requests too (founder, 2026-10-20). */
class RequestsDoorTest {
    @Test
    fun `waiting requests come first`() {
        assertThat(requestsDoorSubtitle(1, "1", hidden = 3)).isEqualTo("1 person is waiting for your answer")
        assertThat(requestsDoorSubtitle(4, "4", hidden = 0)).isEqualTo("4 people are waiting for your answer")
        assertThat(requestsDoorSubtitle(1000, "999+", hidden = 0)).isEqualTo("999+ people are waiting for your answer")
    }

    @Test
    fun `only hidden requests still name themselves`() {
        assertThat(requestsDoorSubtitle(0, "0", hidden = 1)).isEqualTo("1 hidden request")
        assertThat(requestsDoorSubtitle(0, "0", hidden = 2)).isEqualTo("2 hidden requests")
    }

    @Test
    fun `the door shows for hidden requests alone and hides when nothing waits`() {
        assertThat(ChatHomeUiState(serverRequestCount = 0, hiddenRequestCount = 1).showRequestsDoor).isTrue()
        assertThat(ChatHomeUiState(serverRequestCount = 2, hiddenRequestCount = 0).showRequestsDoor).isTrue()
        assertThat(ChatHomeUiState(serverRequestCount = 0, hiddenRequestCount = 0).showRequestsDoor).isFalse()
    }
}
