package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class ChromeVisibilityTest {

    /** A fresh shell shows its bar; nothing has asked otherwise. */
    @Test
    fun `the bar starts visible`() {
        assertThat(ChromeVisibility().bottomBarHidden).isFalse()
    }

    @Test
    fun `a screen can hide the bar and give it back`() {
        val chrome = ChromeVisibility()

        chrome.hideBottomBar(true)
        assertThat(chrome.bottomBarHidden).isTrue()

        chrome.hideBottomBar(false)
        assertThat(chrome.bottomBarHidden).isFalse()
    }
}
