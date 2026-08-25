package com.us.android.core.creator.engine

import android.content.Context
import android.net.Uri
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.io.File
import java.io.FileOutputStream
import java.security.MessageDigest
import java.util.UUID

/**
 * App-private storage for imported originals.
 *
 * ## WHY THE BYTES ARE COPIED RATHER THAN THE URI PERSISTED
 *
 * The legacy composer stored a picker `content://` URI and re-read it later.
 * That works until it doesn't: the grant is temporary, this codebase never took
 * a persistable one, and even a persisted grant can be revoked or the photo
 * deleted from the gallery. A project that promises "your work survives a
 * reboot" cannot rest on any of that.
 *
 * ## CONTAINMENT IS ENFORCED HERE, NOT UPSTREAM
 *
 * Every public method canonicalises its target and refuses anything that does
 * not resolve inside the vault root. Relying on the project document's
 * `vaultPath` pattern is not enough: these methods can be called with a
 * corrupted row, a hand-built id, or before any document exists, and a `..`
 * segment would otherwise let the vault read or DELETE another app-private
 * file. Model validation is a contract; this is a boundary.
 *
 * ## THE QUOTA IS ENFORCED DURING THE STREAM
 *
 * It used to be observational — `importSource` streamed without a limit and
 * `isOverQuota()` reported the damage afterwards. A single large import could
 * therefore blow through the 500 MB promise and keep going until the device ran
 * out of space. The limit is now checked per buffer, so an oversized source is
 * refused mid-stream and its temporary file is removed.
 *
 * ## NOTHING IS READ WHOLE INTO MEMORY
 *
 * Hashing streams through a fixed buffer, on write and on verify. `readBytes()`
 * on a large or corrupt vault entry allocates the entire file and takes the app
 * down with an OOM — on exactly the device class this product targets.
 */
class SourceVault(
    @ApplicationContext private val context: Context,
    private val io: CoroutineDispatcher,
    /**
     * The total the vault may hold. Injectable ONLY so the refusal can be proven.
     *
     * A test that fills 500 MB to prove the limit would be slow and, worse,
     * conditional — it would assert nothing on a machine where the fill itself
     * was refused first. Production always uses [VaultQuota.MAX_BYTES].
     */
    private val maxBytes: Long = VaultQuota.MAX_BYTES,
    /**
     * Free bytes on the vault's filesystem. Injectable so low-storage refusal
     * can be proven deterministically; production reads the real filesystem.
     */
    private val freeBytes: () -> Long = { context.filesDir.usableSpace },
    /**
     * Space that must REMAIN free after an import. Filling a phone to its last
     * byte takes down every other app and usually this one; refusing an import
     * is recoverable, a wedged device is not.
     */
    private val headroomBytes: Long = VaultQuota.HEADROOM_BYTES,
) {

    private val root: File get() = File(context.filesDir, VAULT_DIR)

    /**
     * ONE vault-wide write lock — not per-asset.
     *
     * Per-asset locking was the CS-A-LB-3 race: two imports of DIFFERENT assets
     * ran concurrently, each snapshotted `totalBytes()` before the other had
     * written anything, each passed its own check against that stale snapshot,
     * and together they committed more than the cap. Admission is only atomic if
     * the snapshot, the streaming check and the commit happen under one lock the
     * whole vault shares.
     *
     * The cost is that imports serialise. At launch scale — a person picking up
     * to ten photos — that is invisible; a reservation ledger can replace this
     * if it ever is not. Same-asset races are covered a fortiori.
     */
    private val vaultWriteLock = Mutex()

    /**
     * Absolute location of a relative vault path, or null if it escapes.
     *
     * Null rather than an exception: a corrupted row is a recovery case, and
     * callers already have to handle "this source is not usable".
     */
    fun resolve(relativePath: String): File? {
        if (!VALID_RELATIVE_PATH.matches(relativePath)) return null
        val canonicalRoot = root.canonicalFile
        val target = File(canonicalRoot, relativePath).canonicalFile
        // The separator guard stops `/creator-evil/x` passing a naive prefix test.
        val rootPrefix = canonicalRoot.path + File.separator
        return if (target.path.startsWith(rootPrefix)) target else null
    }

    /**
     * Copy an imported source into the vault.
     *
     * Returns null when the source cannot be read, the asset id is not
     * acceptable, or the import would breach the quota. All three are ordinary
     * outcomes the caller turns into a recovery path, not exceptions.
     */
    suspend fun importSource(uri: Uri, assetId: String): VaultEntry? {
        if (!VALID_ASSET_ID.matches(assetId)) return null
        return vaultWriteLock.withLock { importLocked(uri, assetId) }
    }

    private suspend fun importLocked(uri: Uri, assetId: String): VaultEntry? = withContext(io) {
        val relative = "$SOURCES_DIR/$assetId.bin"
        val target = resolve(relative) ?: return@withContext null
        target.parentFile?.mkdirs()

        // Unique per attempt. A fixed name is a shared mutable file between two
        // callers, and the loser's bytes end up inside the winner's result.
        val temp = File(target.parentFile, "${target.name}.${UUID.randomUUID()}.tmp")

        // Both snapshots are taken UNDER the vault-wide lock, so no other import
        // can commit between this measurement and this import's own commit —
        // that gap was the CS-A-LB-3 race.
        val existingBytes = totalBytesBlocking()

        // Low-storage admission. A device already under the headroom floor gets
        // an immediate refusal, before a single byte is written.
        val freeAtAdmission = freeBytes()
        if (freeAtAdmission <= headroomBytes) return@withContext null
        // The most this import may write while keeping the headroom promise.
        val writableWithinHeadroom = freeAtAdmission - headroomBytes

        val copied = streamWithinBudget(
            uri = uri,
            temp = temp,
            // Neither the vault total nor the device headroom may be exceeded,
            // even briefly, so the budget is whichever bound is tighter.
            budgetBytes = minOf(maxBytes - existingBytes, writableWithinHeadroom),
        )

        if (copied == null) {
            // Never leave a partial file, and never silently delete an older
            // project to make room — that would be trading one piece of the
            // user's work for another without asking.
            temp.delete()
            return@withContext null
        }

        if (!temp.renameTo(target)) {
            temp.delete()
            return@withContext null
        }

        VaultEntry(
            assetId = assetId,
            relativePath = relative,
            sha256 = copied.sha256,
            bytes = copied.bytes,
        )
    }

    private data class StreamedCopy(val bytes: Long, val sha256: String)

    /**
     * Streams the source into [temp], hashing as it goes, refusing at the
     * budget. Null means "not copied": unreadable, empty, or over budget —
     * the caller deletes the temp in every case.
     */
    private fun streamWithinBudget(uri: Uri, temp: File, budgetBytes: Long): StreamedCopy? {
        val digest = MessageDigest.getInstance("SHA-256")
        var bytes = 0L
        var refused = false

        val read = runCatching {
            context.contentResolver.openInputStream(uri)?.use { input ->
                FileOutputStream(temp).use { output ->
                    val buffer = ByteArray(BUFFER_BYTES)
                    while (true) {
                        val count = input.read(buffer)
                        if (count <= 0) break
                        bytes += count
                        // Checked per buffer, BEFORE the write.
                        if (bytes > budgetBytes) {
                            refused = true
                            return@use
                        }
                        digest.update(buffer, 0, count)
                        output.write(buffer, 0, count)
                    }
                    output.fd.sync()
                }
                true
            } ?: false
        }.getOrDefault(false)

        if (refused || !read || bytes == 0L) return null
        return StreamedCopy(
            bytes = bytes,
            sha256 = digest.digest().joinToString("") { "%02x".format(it) },
        )
    }

    /**
     * Verify a vault file still matches what the project recorded.
     *
     * Streams. A vault entry can be tens of megabytes and this runs on load, so
     * reading it whole would allocate the file on the main path of opening a
     * project.
     */
    suspend fun verify(relativePath: String, expectedSha256: String): Boolean = withContext(io) {
        val file = resolve(relativePath) ?: return@withContext false
        if (!file.isFile) return@withContext false
        val actual = runCatching { streamingSha256(file) }.getOrNull()
        actual == expectedSha256
    }

    suspend fun delete(relativePath: String): Boolean = withContext(io) {
        val file = resolve(relativePath) ?: return@withContext false
        file.delete()
    }

    /** Total bytes held, for the quota check and for retention instrumentation. */
    suspend fun totalBytes(): Long = withContext(io) { totalBytesBlocking() }

    suspend fun isOverQuota(): Boolean = totalBytes() > maxBytes

    private fun totalBytesBlocking(): Long =
        root.walkTopDown().filter { it.isFile }.sumOf { it.length() }

    private fun streamingSha256(file: File): String {
        val digest = MessageDigest.getInstance("SHA-256")
        file.inputStream().use { input ->
            val buffer = ByteArray(BUFFER_BYTES)
            while (true) {
                val count = input.read(buffer)
                if (count <= 0) break
                digest.update(buffer, 0, count)
            }
        }
        return digest.digest().joinToString("") { "%02x".format(it) }
    }

    private companion object {
        const val VAULT_DIR = "creator"
        const val SOURCES_DIR = "sources"
        const val BUFFER_BYTES = 64 * 1024

        /** Mirrors the frozen `vaultPath` contract; no separators, no traversal. */
        val VALID_RELATIVE_PATH = Regex("^(sources|proxies|outputs)/[A-Za-z0-9_-]{1,64}\\.bin$")

        /** An asset id may not contain a separator or a dot, so it cannot build a path. */
        val VALID_ASSET_ID = Regex("^[A-Za-z0-9_-]{1,64}$")
    }
}

data class VaultEntry(
    val assetId: String,
    /** Relative. The absolute path exists only inside [SourceVault]. */
    val relativePath: String,
    val sha256: String,
    val bytes: Long,
)

object VaultQuota {
    /**
     * 500 MB, enforced during the stream rather than reported afterwards.
     *
     * Over quota, the import is REFUSED. Nothing older is deleted to make room:
     * the vault holds the only copy of work the user may not have published
     * anywhere else, so freeing space is a decision for them, not for this class.
     */
    const val MAX_BYTES = 500L * 1024 * 1024

    /**
     * 50 MB must stay free after any import.
     *
     * Filling the filesystem to zero does not fail one import cleanly — it
     * breaks SQLite journaling, other apps, and usually the OS UI. The refusal
     * threshold is deliberately far above "actually full".
     */
    const val HEADROOM_BYTES = 50L * 1024 * 1024
}
