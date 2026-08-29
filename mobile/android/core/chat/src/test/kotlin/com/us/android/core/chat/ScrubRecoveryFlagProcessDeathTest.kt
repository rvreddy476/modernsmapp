package com.us.android.core.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.di.ChatModule
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File

/**
 * The PRODUCTION recovery-marker medium across process recreation (review
 * blocker 1). Every "fresh process" below is a NEW flag instance created by
 * the real DI factory over the same on-disk state — the closest a JVM test
 * can get to killing and recreating the process at a write boundary.
 *
 * The marker is INVERTED (what is persisted is "verifiably clean", in TWO
 * media, AND-combined), so the owed state never depends on a durable WRITE
 * succeeding: every loss sequence the review named — `commit()` returning
 * false, process death between the two marker writes, both media failing on
 * a full disk — leaves the clean claim broken or absent, which a fresh
 * process reads as OWED and repays before opening chat.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ScrubRecoveryFlagProcessDeathTest {

    private lateinit var context: Context

    private val prefs
        get() = context.getSharedPreferences("chat_maintenance", Context.MODE_PRIVATE)

    private val cleanFile
        get() = File(context.filesDir, "chat_scrub_clean")

    /** A "rebooted process": a brand-new flag over the same disk state. */
    private fun freshProcess() = ChatModule.provideScrubRecoveryFlag(context)

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        prefs.edit().clear().commit()
        cleanFile.delete()
    }

    @Test
    fun `a process with no marker history boots OWED - the fail-secure default`() {
        // Death before ANY marker write ever ran (the review's sequence 1:
        // commit() failed and the process died before the second write).
        // With the inverted marker there is nothing to lose: no clean claim
        // exists, so the fresh process is owed.
        assertThat(freshProcess().isPending()).isTrue()
    }

    @Test
    fun `clean and owed both survive process recreation`() {
        assertThat(freshProcess().setPending(false)).isTrue() // scrub verified clean
        assertThat(freshProcess().isPending()).isFalse()

        assertThat(freshProcess().setPending(true)).isTrue() // logout owes again
        assertThat(freshProcess().isPending()).isTrue()
    }

    @Test
    fun `death between the two owed-marking deletions still reads OWED`() {
        freshProcess().setPending(false)

        // Owed-marking clears the preference first, then the file. Simulate
        // process death BETWEEN the two: only the preference write happened.
        prefs.edit().putBoolean("scrub_clean", false).commit()
        assertThat(cleanFile.exists()).isTrue() // the file half never ran

        // Clean requires BOTH media, so the half-marked state is owed.
        assertThat(freshProcess().isPending()).isTrue()
    }

    @Test
    fun `death between the two clean-marking writes still reads OWED`() {
        // Clean-marking writes both media; death after only ONE landed must
        // NOT read as clean — the scrub verdict was never fully recorded.
        prefs.edit().putBoolean("scrub_clean", true).commit()
        assertThat(freshProcess().isPending()).isTrue()

        prefs.edit().clear().commit()
        cleanFile.createNewFile()
        assertThat(freshProcess().isPending()).isTrue()
    }

    @Test
    fun `marking owed succeeds while a clean claim exists in only one medium`() {
        // Degraded state: one stale clean medium. Marking owed must still
        // report durable success (one broken medium suffices — clean is the
        // AND) and a fresh process must read owed.
        cleanFile.createNewFile()
        assertThat(freshProcess().setPending(true)).isTrue()
        assertThat(freshProcess().isPending()).isTrue()
        assertThat(cleanFile.exists()).isFalse()
    }
}
