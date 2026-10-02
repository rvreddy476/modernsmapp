package com.us.android.core.media.offline

import android.content.Context
import android.os.Environment
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import java.io.File

/**
 * WHERE offline copies are stored.
 *
 * founder, 2026-10-02: "Keep a copy is not direct download ... Nobody can
 * see download location." What this protects is the whole of that promise
 * on the device: the copies' directory is inside the app's no-backup
 * private storage, and is never external or shared storage, never the
 * backed-up `files` directory, never the cache the OS may hand back, and
 * never reachable by walking out of the private directory with `..`.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class OfflineStorageTest {

    private val context: Context = RuntimeEnvironment.getApplication()

    private fun sharedRoots(): List<File> = buildList {
        addAll(context.getExternalFilesDirs(null).filterNotNull())
        addAll(context.externalCacheDirs.filterNotNull())
        @Suppress("DEPRECATION")
        add(Environment.getExternalStorageDirectory())
    }

    private fun File.isUnder(parent: File): Boolean = canonicalPath.startsWith(parent.canonicalPath + File.separator)

    @Test
    fun `the copies directory is inside the app's no-backup private storage`() {
        val root = OfflineStorage(context).root

        assertThat(root.isUnder(context.noBackupFilesDir)).isTrue()
        assertThat(root.name).isEqualTo(OFFLINE_DIR_NAME)
        assertThat(root).isEqualTo(offlineCopiesDir(context.noBackupFilesDir))
    }

    @Test
    fun `the copies directory is never external or shared storage`() {
        val root = OfflineStorage(context).root

        for (shared in sharedRoots()) {
            assertThat(root.isUnder(shared)).isFalse()
            assertThat(root.canonicalFile).isNotEqualTo(shared.canonicalFile)
        }
    }

    /** `files` is what a backup and a device transfer copy; `cache` is what the OS may delete. Neither holds a copy. */
    @Test
    fun `the copies directory is not the backed-up files directory nor the cache`() {
        val root = OfflineStorage(context).root

        assertThat(root.isUnder(context.filesDir)).isFalse()
        assertThat(root.isUnder(context.cacheDir)).isFalse()
    }

    @Test
    fun `everything a copy is made of lives inside that one directory`() {
        val storage = OfflineStorage(context)

        for (place in listOf(storage.mediaDir, storage.databaseFile, storage.filesDir, storage.indexFile)) {
            assertThat(place.isUnder(storage.root)).isTrue()
        }
    }

    // ── The guard, as a rule ────────────────────────────────────────────

    private val noBackup = File("/data/user/0/app/no_backup")
    private val external = listOf(File("/storage/emulated/0"), File("/storage/emulated/0/Android/data/app/files"))

    @Test
    fun `a directory inside no-backup storage is private`() {
        assertThat(isPrivateOfflineDir(File(noBackup, "offline_copies"), noBackup, external)).isTrue()
    }

    @Test
    fun `external and shared directories are refused`() {
        val refused = listOf(
            File("/storage/emulated/0/Download/offline_copies"),
            File("/storage/emulated/0/Movies"),
            File("/storage/emulated/0/Android/data/app/files/offline_copies"),
            File("/storage/emulated/0"),
        )
        for (dir in refused) {
            assertThat(isPrivateOfflineDir(dir, noBackup, external)).isFalse()
        }
    }

    @Test
    fun `the app's other private directories are refused too`() {
        val refused = listOf(
            File("/data/user/0/app/files/offline_copies"),
            File("/data/user/0/app/cache/offline_copies"),
            // The no-backup directory itself is not INSIDE it.
            noBackup,
        )
        for (dir in refused) {
            assertThat(isPrivateOfflineDir(dir, noBackup, external)).isFalse()
        }
    }

    @Test
    fun `a path that walks out of no-backup storage with dot-dot is refused`() {
        val escaped = File(noBackup, "../files/offline_copies")

        assertThat(isPrivateOfflineDir(escaped, noBackup, external)).isFalse()
    }
}
