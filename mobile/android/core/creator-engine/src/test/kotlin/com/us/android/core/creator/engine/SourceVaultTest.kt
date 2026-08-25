package com.us.android.core.creator.engine

import android.content.Context
import android.net.Uri
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import java.io.File

/**
 * CS-LB-1 — imported sources are durable, and the vault never leaks a location.
 *
 * ## WHY THIS TEST USES A REAL FILESYSTEM
 *
 * The claim is "the user's photo is still there after a reboot". A fake that
 * records calls proves the copy was requested; only real bytes on a real disk,
 * re-hashed on the way back, prove it landed. Robolectric gives a real
 * `filesDir`, so that is what this uses.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class SourceVaultTest {

    private lateinit var context: Context

    private val photoBytes = "android-creator-project-v1/fixture-asset/a1".toByteArray()

    /** SHA-256 of [photoBytes] — the same deterministic asset the fixtures use. */
    private val photoSha = "425b14bf90238cd23ec018a45c65b07ba8819e8dfa07a5bf184dc0eb3d1a9abf"

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        File(context.filesDir, "creator").deleteRecursively()
    }

    /**
     * The vault's IO dispatcher must share runTest's scheduler.
     *
     * Constructing it in @Before with its own StandardTestDispatcher creates a
     * second scheduler, and kotlinx-coroutines rejects that outright rather
     * than let two virtual clocks drift apart.
     */
    private fun TestScope.vault() = SourceVault(context, StandardTestDispatcher(testScheduler))

    /** Registers bytes at a content:// URI the way the photo picker would. */
    private fun pickerUri(bytes: ByteArray): Uri {
        val uri = Uri.parse("content://media/external/images/media/42")
        Shadows.shadowOf(context.contentResolver).registerInputStream(uri, bytes.inputStream())
        return uri
    }

    @Test
    fun `an imported source lands in the vault with its real hash and size`() = runTest {
        val vault = vault()
        val entry = vault.importSource(pickerUri(photoBytes), assetId = "a1")

        assertThat(entry).isNotNull()
        assertThat(entry!!.sha256).isEqualTo(photoSha)
        assertThat(entry.bytes).isEqualTo(photoBytes.size.toLong())
        assertThat(vault.resolve(entry.relativePath)!!.readBytes()).isEqualTo(photoBytes)
    }

    /**
     * The stored path is RELATIVE.
     *
     * An absolute path or a `content://` URI in the project document is both a
     * durability bug and a privacy leak waiting to be serialized into a request
     * or a crash report. V-15 checks the document; this checks the source.
     */
    @Test
    fun `the recorded path is relative and carries no scheme or device location`() = runTest {
        val vault = vault()
        val entry = vault.importSource(pickerUri(photoBytes), assetId = "a1")!!

        assertThat(entry.relativePath).isEqualTo("sources/a1.bin")
        assertThat(entry.relativePath).doesNotContain("://")
        assertThat(entry.relativePath).doesNotContain("/data/")
        assertThat(entry.relativePath.startsWith("/")).isFalse()
    }

    /**
     * A revoked grant is a NULL, not a crash.
     *
     * The user deleting a photo from their gallery is an ordinary thing that
     * happens outside the app. It must produce a recovery path, not a stack
     * trace.
     */
    @Test
    fun `an unreadable source returns null instead of throwing`() = runTest {
        val vault = vault()
        val missing = Uri.parse("content://media/external/images/media/does-not-exist")

        assertThat(vault.importSource(missing, assetId = "a1")).isNull()
    }

    /** An interrupted import leaves no `.tmp` claiming to be a real source. */
    @Test
    fun `a failed import leaves no partial file behind`() = runTest {
        val vault = vault()
        vault.importSource(Uri.parse("content://media/nope"), assetId = "a1")

        val sources = File(context.filesDir, "creator/sources")
        val leftovers = sources.listFiles()?.toList().orEmpty()
        assertThat(leftovers).isEmpty()
    }

    /**
     * Verification catches a file that changed underneath the project.
     *
     * That is the difference between exporting the user's photo and exporting
     * something else, and only the recorded hash knows which.
     */
    @Test
    fun `verify accepts the original bytes and rejects altered ones`() = runTest {
        val vault = vault()
        val entry = vault.importSource(pickerUri(photoBytes), assetId = "a1")!!

        assertThat(vault.verify(entry.relativePath, entry.sha256)).isTrue()

        vault.resolve(entry.relativePath)!!.writeBytes("something else entirely".toByteArray())

        assertThat(vault.verify(entry.relativePath, entry.sha256)).isFalse()
    }

    @Test
    fun `verify rejects a path with no file at all`() = runTest {
        val vault = vault()
        assertThat(vault.verify("sources/never-written.bin", photoSha)).isFalse()
    }

    @Test
    fun `totalBytes reports what the vault is actually holding`() = runTest {
        val vault = vault()
        vault.importSource(pickerUri(photoBytes), assetId = "a1")
        vault.importSource(pickerUri(photoBytes), assetId = "a2")

        assertThat(vault.totalBytes()).isEqualTo(photoBytes.size.toLong() * 2)
        assertThat(vault.isOverQuota()).isFalse()
    }

    @Test
    fun `a deleted source is gone from the vault`() = runTest {
        val vault = vault()
        val entry = vault.importSource(pickerUri(photoBytes), assetId = "a1")!!

        assertThat(vault.delete(entry.relativePath)).isTrue()
        assertThat(vault.resolve(entry.relativePath)!!.exists()).isFalse()
    }
}
