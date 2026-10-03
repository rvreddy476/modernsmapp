package com.us.android.core.feed.offline

import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import java.io.File
import java.io.IOException

/**
 * The index file's whole content: ONE account's copies in front ([ownerId],
 * [copies]), and every other account's behind it ([held]).
 *
 * Version 1 had only the front set; it reads as a version 2 with nothing
 * held and no stamp.
 */
@Serializable
data class OfflineIndexFile(
    val version: Int = 2,
    /** Whose copies these are. A different viewer on this device never sees or plays them. */
    val ownerId: String = "",
    /** The `device_id` the copies were granted under: what a remove must name, even after settings were cleared. */
    val deviceId: String = "",
    val copies: List<OfflineCopy> = emptyList(),
    /**
     * When [ownerId] signed out (or was found signed out), on this device's
     * clock; null while they are signed in. Stamped copies are shown to
     * nobody, and are deleted once the stamp is 48 hours old.
     */
    val signedOutAtMs: Long? = null,
    /** Other accounts' copies, each waiting out its own 48 hours. Never listed, played or counted. */
    val held: List<OfflineHeldSet> = emptyList(),
)

/** One signed-out account's copies, kept apart from whoever is signed in now. */
@Serializable
data class OfflineHeldSet(
    val ownerId: String,
    val deviceId: String = "",
    val signedOutAtMs: Long,
    val copies: List<OfflineCopy> = emptyList(),
)

/**
 * The list of offline copies on this device: one small JSON file BESIDE the
 * bytes, in the app's no-backup directory (2026-10-02).
 *
 * Not a Room table, on purpose. The shared database lives in the app's
 * `databases` directory, which a device-to-device transfer copies while it
 * leaves `no_backup` behind: an index that arrived on a new phone without
 * its bytes would list copies that are not there. Kept beside the bytes,
 * the index and the copies come and go together.
 *
 * A file that cannot be read is an empty index, never a crash: the worst
 * outcome is copies the app no longer lists, which the next wipe removes.
 * Written to a temporary file and renamed, so a kill mid-write leaves the
 * previous index.
 */
class OfflineIndex(
    private val file: () -> File,
    private val json: Json = Json { ignoreUnknownKeys = true },
) {
    fun read(): OfflineIndexFile {
        val target = file()
        if (!target.exists()) return OfflineIndexFile()
        return try {
            json.decodeFromString(OfflineIndexFile.serializer(), target.readText())
        } catch (_: SerializationException) {
            OfflineIndexFile()
        } catch (_: IllegalArgumentException) {
            OfflineIndexFile()
        } catch (_: IOException) {
            OfflineIndexFile()
        }
    }

    /** True when the index is on disk. A failed write leaves the previous one. */
    fun write(index: OfflineIndexFile): Boolean {
        val target = file()
        val temp = File(target.parentFile, target.name + TEMP_SUFFIX)
        return try {
            target.parentFile?.mkdirs()
            temp.writeText(json.encodeToString(OfflineIndexFile.serializer(), index))
            if (target.exists()) target.delete()
            temp.renameTo(target)
        } catch (_: IOException) {
            temp.delete()
            false
        }
    }

    fun delete() {
        file().delete()
    }

    private companion object {
        const val TEMP_SUFFIX = ".tmp"
    }
}
