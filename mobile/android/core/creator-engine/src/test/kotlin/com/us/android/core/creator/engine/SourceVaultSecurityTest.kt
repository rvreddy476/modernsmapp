package com.us.android.core.creator.engine

import android.content.Context
import android.net.Uri
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
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
 * CS-A-LB-3 — the vault's containment and quota contracts are enforced, not
 * merely described.
 *
 * ## WHAT WAS WRONG
 *
 * `resolve` was `File(root, relativePath)` with no validation, and `verify` and
 * `delete` accepted any string. A `../` segment escaped `filesDir/creator` and
 * let the vault read — or DELETE — another app-private file. The 500 MB limit
 * was observational: `importSource` streamed without a bound and `isOverQuota()`
 * reported the breach afterwards. `verify` used `readBytes()`, so one large or
 * corrupt entry could take the app down with an OOM. And concurrent imports of
 * one asset shared a single fixed `.tmp` name.
 *
 * Each test below targets one of those, and each corresponds to a negative
 * control recorded in the correction pass.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class SourceVaultSecurityTest {

    private lateinit var context: Context

    private val photoBytes = "android-creator-project-v1/fixture-asset/a1".toByteArray()

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        File(context.filesDir, "creator").deleteRecursively()
    }

    private fun TestScope.vault() = SourceVault(context, StandardTestDispatcher(testScheduler))

    private fun TestScope.boundedVault(maxBytes: Long) =
        SourceVault(context, StandardTestDispatcher(testScheduler), maxBytes)

    private fun tempFiles(): List<File> =
        File(context.filesDir, "creator/sources").listFiles().orEmpty()
            .filter { it.name.contains(".tmp") }

    private fun pickerUri(bytes: ByteArray, id: String = "42"): Uri {
        val uri = Uri.parse("content://media/external/images/media/$id")
        Shadows.shadowOf(context.contentResolver).registerInputStream(uri, bytes.inputStream())
        return uri
    }

    /** A file that belongs to the app but NOT to the vault. */
    private fun plantOutsideVault(name: String, content: String): File {
        val file = File(context.filesDir, name)
        file.parentFile?.mkdirs()
        file.writeText(content)
        return file
    }

    // ------------------------------------------------------------------
    // Path containment
    // ------------------------------------------------------------------

    /**
     * THE ESCAPE.
     *
     * `sources/../../secrets.txt` canonicalises outside the vault. Before the
     * fix this resolved to a real file the vault had no business touching.
     */
    @Test
    fun `a traversal path does not resolve`() = runTest {
        val vault = vault()

        assertThat(vault.resolve("sources/../../secrets.bin")).isNull()
        assertThat(vault.resolve("../secrets.bin")).isNull()
        assertThat(vault.resolve("/data/data/other/secrets.bin")).isNull()
        assertThat(vault.resolve("sources/../proxies/a1.bin")).isNull()
    }

    /** A traversal path cannot DELETE another app-private file. */
    @Test
    fun `a traversal path cannot delete a file outside the vault`() = runTest {
        val vault = vault()
        val victim = plantOutsideVault("important.txt", "the user's other data")

        val deleted = vault.delete("sources/../../important.txt")

        assertThat(deleted).isFalse()
        assertThat(victim.exists()).isTrue()
        assertThat(victim.readText()).isEqualTo("the user's other data")
    }

    /** A traversal path cannot be verified — so it cannot be used to probe for files. */
    @Test
    fun `a traversal path cannot read a file outside the vault`() = runTest {
        val vault = vault()
        plantOutsideVault("important.txt", "the user's other data")

        assertThat(vault.verify("sources/../../important.txt", "a".repeat(64))).isFalse()
    }

    /** An asset id carrying a separator cannot be used to build a path. */
    @Test
    fun `an asset id containing a separator is refused`() = runTest {
        val vault = vault()

        assertThat(vault.importSource(pickerUri(photoBytes), assetId = "../escape")).isNull()
        assertThat(vault.importSource(pickerUri(photoBytes), assetId = "a/b")).isNull()
        assertThat(vault.importSource(pickerUri(photoBytes), assetId = "a.bin")).isNull()
        assertThat(vault.importSource(pickerUri(photoBytes), assetId = "")).isNull()
    }

    /** A legitimate path still resolves — containment must not break the vault. */
    @Test
    fun `a valid relative path resolves inside the vault`() = runTest {
        val vault = vault()

        val resolved = vault.resolve("sources/a1.bin")

        assertThat(resolved).isNotNull()
        assertThat(resolved!!.canonicalPath)
            .startsWith(File(context.filesDir, "creator").canonicalPath)
    }

    // ------------------------------------------------------------------
    // Quota, enforced during the stream
    // ------------------------------------------------------------------

    /**
     * An oversized source is refused, and leaves nothing behind.
     *
     * The quota is deliberately lowered for the test rather than allocating
     * 500 MB: what is being proven is that the check happens per buffer and
     * aborts the write, not the specific constant.
     */
    @Test
    fun `an import that would breach the quota is refused and leaves no file`() = runTest {
        // A small limit so the refusal is unconditional and fast. Production
        // uses VaultQuota.MAX_BYTES; the mechanism under test is the per-buffer
        // check, not the constant.
        val vault = boundedVault(maxBytes = SMALL_QUOTA)
        val oversized = ByteArray(SMALL_QUOTA.toInt() + 1) { 1 }

        val entry = vault.importSource(pickerUri(oversized, "big"), assetId = "big")

        assertThat(entry).isNull()
        assertThat(vault.resolve("sources/big.bin")!!.exists()).isFalse()
        // No temp file survived the refusal.
        assertThat(tempFiles()).isEmpty()
        // And nothing was written at all — the vault is still empty.
        assertThat(vault.totalBytes()).isEqualTo(0L)
    }

    /** An import that fits is still accepted — the limit must not block ordinary work. */
    @Test
    fun `an import within the quota is accepted`() = runTest {
        val vault = boundedVault(maxBytes = SMALL_QUOTA)

        val entry = vault.importSource(pickerUri(photoBytes), assetId = "a1")

        assertThat(entry).isNotNull()
        assertThat(vault.isOverQuota()).isFalse()
    }

    /**
     * A refusal does not delete older work to make room.
     *
     * The vault holds the only copy of work the user may not have published
     * anywhere else. Freeing space is their decision, not this class's.
     */
    @Test
    fun `a refused import leaves existing entries untouched`() = runTest {
        val vault = boundedVault(maxBytes = SMALL_QUOTA)
        val existing = vault.importSource(pickerUri(photoBytes, "keep"), assetId = "keep")!!
        val heldBefore = vault.totalBytes()

        vault.importSource(pickerUri(ByteArray(SMALL_QUOTA.toInt()) { 1 }, "big"), assetId = "big")

        assertThat(vault.totalBytes()).isEqualTo(heldBefore)
        assertThat(vault.verify(existing.relativePath, existing.sha256)).isTrue()
    }

    // ------------------------------------------------------------------
    // Streaming hash
    // ------------------------------------------------------------------

    /**
     * Verification is bounded.
     *
     * A multi-megabyte entry must not be read whole into memory. This proves the
     * result is still correct at a size where `readBytes()` would have allocated
     * the entire file; the bound itself is the buffer in `streamingSha256`.
     */
    @Test
    fun `a large entry verifies without being read whole`() = runTest {
        val vault = vault()
        val large = ByteArray(LARGE_BYTES) { (it % 251).toByte() }

        val entry = vault.importSource(pickerUri(large, "large"), assetId = "large")!!

        assertThat(entry.bytes).isEqualTo(LARGE_BYTES.toLong())
        assertThat(vault.verify(entry.relativePath, entry.sha256)).isTrue()
        assertThat(vault.verify(entry.relativePath, "0".repeat(64))).isFalse()
    }

    // ------------------------------------------------------------------
    // Concurrent imports of one asset
    // ------------------------------------------------------------------

    /**
     * Two imports of the same asset id produce ONE intact file.
     *
     * With a shared fixed `.tmp` name the two writers interleaved into one file
     * and the survivor could match neither hash. Here each attempt writes a
     * unique temp and same-asset imports are serialised, so whichever wins, the
     * recorded hash describes the bytes on disk.
     */
    @Test
    fun `concurrent imports of one asset produce a single hash-matching result`() = runTest {
        val vault = vault()

        val results = listOf(
            async { vault.importSource(pickerUri(photoBytes, "c1"), assetId = "shared") },
            async { vault.importSource(pickerUri(photoBytes, "c2"), assetId = "shared") },
        ).awaitAll()

        val entries = results.filterNotNull()
        assertThat(entries).isNotEmpty()
        entries.forEach { entry ->
            assertThat(vault.verify(entry.relativePath, entry.sha256)).isTrue()
        }
        // Exactly one final file, and no temp left over.
        val files = File(context.filesDir, "creator/sources").listFiles().orEmpty()
        assertThat(files.filter { !it.name.contains(".tmp") }).hasSize(1)
        assertThat(files.filter { it.name.contains(".tmp") }).isEmpty()
    }

    private companion object {
        const val LARGE_BYTES = 3 * 1024 * 1024
        const val SMALL_QUOTA = 8L * 1024
    }
}
