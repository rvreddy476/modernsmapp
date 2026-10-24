package com.us.android.core.chat.hidden

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * The reveal and open tokens live in memory and go on every re-lock rule:
 * one minute idle, background, sign-out, the server refusing them, expiry.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class HiddenTokenStoreTest {

    private var clock = 1_000_000L
    private fun store(idleMs: Long = HIDDEN_IDLE_MS) = HiddenTokenStore(now = { clock }, timerScope = null, idleMs = idleMs)

    @Test
    fun `a new store starts locked — nothing survives a process`() {
        val s = store()
        assertThat(s.reveal()).isNull()
        assertThat(s.open()).isNull()
        assertThat(s.snapshot.value).isEqualTo(HiddenTokenSnapshot())
        assertThat(s.lockedIds.value).isEmpty()
    }

    @Test
    fun `the snapshot says whether, never what`() {
        val s = store()
        s.setReveal("reveal-secret", null)
        s.setOpen("open-secret", null)
        assertThat(s.snapshot.value).isEqualTo(HiddenTokenSnapshot(revealed = true, opened = true, lastDrop = null))
        assertThat(s.snapshot.value.toString()).doesNotContain("secret")
        assertThat(s.reveal()).isEqualTo("reveal-secret")
        assertThat(s.open()).isEqualTo("open-secret")
    }

    @Test
    fun `a blank token is no token`() {
        val s = store()
        s.setReveal("   ", null)
        assertThat(s.reveal()).isNull()
        assertThat(s.snapshot.value.revealed).isFalse()
    }

    @Test
    fun `one minute without activity drops both tokens`() {
        val s = store()
        s.setReveal("r", null)
        s.setOpen("o", null)
        clock += HIDDEN_IDLE_MS - 1
        assertThat(s.checkIdle()).isFalse()
        assertThat(s.open()).isEqualTo("o")
        clock += 1
        assertThat(s.checkIdle()).isTrue()
        assertThat(s.reveal()).isNull()
        assertThat(s.open()).isNull()
        assertThat(s.snapshot.value.lastDrop).isEqualTo(HiddenDropReason.Idle)
    }

    @Test
    fun `activity restarts the idle clock`() {
        val s = store()
        s.setOpen("o", null)
        clock += HIDDEN_IDLE_MS - 1_000
        s.touch()
        clock += HIDDEN_IDLE_MS - 1_000
        assertThat(s.checkIdle()).isFalse()
        assertThat(s.open()).isEqualTo("o")
    }

    @Test
    fun `the idle timer drops on its own`() = runTest {
        val s = HiddenTokenStore(now = { testScheduler.currentTime }, timerScope = backgroundScope)
        s.setReveal("r", null)
        advanceTimeBy(HIDDEN_IDLE_MS - 1)
        runCurrent()
        assertThat(s.snapshot.value.revealed).isTrue()
        advanceTimeBy(2)
        runCurrent()
        assertThat(s.snapshot.value.revealed).isFalse()
        assertThat(s.snapshot.value.lastDrop).isEqualTo(HiddenDropReason.Idle)
    }

    @Test
    fun `the timer re-aims after activity instead of dropping early`() = runTest {
        val s = HiddenTokenStore(now = { testScheduler.currentTime }, timerScope = backgroundScope)
        s.setOpen("o", null)
        advanceTimeBy(HIDDEN_IDLE_MS / 2)
        s.touch()
        advanceTimeBy(HIDDEN_IDLE_MS / 2 + 10)
        runCurrent()
        assertThat(s.open()).isEqualTo("o")
        advanceTimeBy(HIDDEN_IDLE_MS)
        runCurrent()
        assertThat(s.open()).isNull()
    }

    @Test
    fun `background and lock drop both`() {
        val s = store()
        s.setReveal("r", null)
        s.setOpen("o", null)
        s.drop(HiddenDropReason.Background)
        assertThat(s.reveal()).isNull()
        assertThat(s.open()).isNull()
        assertThat(s.snapshot.value.lastDrop).isEqualTo(HiddenDropReason.Background)
    }

    @Test
    fun `a refused open token goes alone, the list stays revealed`() {
        val s = store()
        s.setReveal("r", null)
        s.setOpen("o", null)
        s.drop(HiddenDropReason.Server, openOnly = true)
        assertThat(s.open()).isNull()
        assertThat(s.reveal()).isEqualTo("r")
        assertThat(s.snapshot.value).isEqualTo(HiddenTokenSnapshot(revealed = true, opened = false, lastDrop = HiddenDropReason.Server))
    }

    @Test
    fun `dropping nothing announces nothing`() {
        val s = store()
        s.drop(HiddenDropReason.Lock)
        assertThat(s.snapshot.value.lastDrop).isNull()
    }

    @Test
    fun `sign-out forgets the tokens and every hidden id`() {
        val s = store()
        s.setReveal("r", null)
        s.markLocked(listOf("c1", "c2"))
        s.forgetAll()
        assertThat(s.reveal()).isNull()
        assertThat(s.lockedIds.value).isEmpty()
        assertThat(s.isLocked("c1")).isFalse()
        assertThat(s.snapshot.value.lastDrop).isEqualTo(HiddenDropReason.SignOut)
    }

    @Test
    fun `a re-lock keeps the hidden ids — they keep chats out of the normal list`() {
        val s = store()
        s.setReveal("r", null)
        s.markLocked(listOf("c1"))
        s.drop(HiddenDropReason.Idle)
        assertThat(s.isLocked("c1")).isTrue()
        s.unmarkLocked("c1")
        assertThat(s.isLocked("c1")).isFalse()
        assertThat(s.isLocked(null)).isFalse()
        assertThat(s.isLocked("")).isFalse()
    }

    @Test
    fun `an expired token is dropped when asked for`() {
        val s = store(idleMs = Long.MAX_VALUE)
        s.setOpen("o", (clock / 1000 + 60).toString())
        clock += 59_000
        assertThat(s.open()).isEqualTo("o")
        clock += 2_000
        assertThat(s.open()).isNull()
        assertThat(s.snapshot.value.lastDrop).isEqualTo(HiddenDropReason.Expired)
    }

    @Test
    fun `a successful call slides the expiry`() {
        val s = store(idleMs = Long.MAX_VALUE)
        s.setReveal("r", null)
        clock += HIDDEN_TOKEN_TTL_MS - 1_000
        s.slide(isOpen = false)
        clock += HIDDEN_TOKEN_TTL_MS - 1_000
        assertThat(s.reveal()).isEqualTo("r")
        clock += 2_000
        assertThat(s.reveal()).isNull()
    }

    @Test
    fun `sliding never shortens`() {
        val s = store(idleMs = Long.MAX_VALUE)
        s.setOpen("o", null)
        s.slide(isOpen = true, expiresAtHeader = (clock / 1000 + 1).toString())
        clock += 60_000
        assertThat(s.open()).isEqualTo("o")
    }
}
