package com.us.android.core.media.offline

import android.content.Context
import android.os.Environment
import dagger.hilt.android.qualifiers.ApplicationContext
import java.io.File
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Where offline copies live on the device (2026-10-02).
 *
 * founder, 2026-10-02: "Keep a copy is not direct download. It should be
 * like to see offline in the app only like YouTube, TikTok or Instagram.
 * Nobody can see download location."
 *
 * So the bytes go to ONE directory, [offlineCopiesDir], under the app's
 * `noBackupFilesDir`:
 *
 *  - app-private internal storage: no other app, no file manager and no
 *    gallery can read it, and the media scanner never walks it;
 *  - `no_backup`: the platform leaves it out of cloud backup AND out of a
 *    device-to-device transfer, whatever the manifest's rules say, so a
 *    copy cannot follow the account to another phone without the server
 *    granting it there;
 *  - never under a `FileProvider` path (`res/xml/create_capture_paths.xml`
 *    lists cache paths only), so no content URI for a copy can exist.
 *
 * Nothing in this package hands out a path for showing, sharing or opening
 * elsewhere. [root] refuses to resolve if the directory is ever anywhere
 * else ([isPrivateOfflineDir]): a misplaced copy is a published file.
 */
@Singleton
class OfflineStorage @Inject constructor(
    @ApplicationContext private val context: Context,
) {
    /** The one directory. Created on first use. */
    val root: File by lazy {
        val dir = offlineCopiesDir(context.noBackupFilesDir)
        check(isPrivateOfflineDir(dir, context.noBackupFilesDir, sharedRoots())) {
            "offline copies must stay in app-private no-backup storage"
        }
        dir.apply { mkdirs() }
    }

    /** Media3's cache of the video and sound bytes. */
    val mediaDir: File get() = File(root, MEDIA_DIR)

    /** Media3's index of what the cache holds, beside the bytes so one wipe takes both. */
    val databaseFile: File get() = File(root, DATABASE_FILE)

    /** Posters and caption files, one folder per post. */
    val filesDir: File get() = File(root, FILES_DIR)

    /** The list of copies the app keeps (`:core:feed` owns its shape). */
    val indexFile: File get() = File(root, INDEX_FILE)

    /** Free space where the copies go. */
    fun usableBytes(): Long = root.usableSpace

    private fun sharedRoots(): List<File> = buildList {
        addAll(context.getExternalFilesDirs(null).filterNotNull())
        addAll(context.externalCacheDirs.filterNotNull())
        @Suppress("DEPRECATION") // Only compared against, never written to.
        Environment.getExternalStorageDirectory()?.let(::add)
    }

    private companion object {
        const val MEDIA_DIR = "media"
        const val DATABASE_FILE = "media_index.db"
        const val FILES_DIR = "files"
        const val INDEX_FILE = "copies.json"
    }
}

/** The copies' directory: always a child of the app's no-backup directory. */
fun offlineCopiesDir(noBackupFilesDir: File): File = File(noBackupFilesDir, OFFLINE_DIR_NAME)

/**
 * True only when [dir] is INSIDE [noBackupFilesDir] and inside none of
 * [sharedRoots] (external and shared storage). Canonical paths, so a `..`
 * cannot walk a copy out of the private directory.
 */
fun isPrivateOfflineDir(dir: File, noBackupFilesDir: File, sharedRoots: List<File>): Boolean {
    val target = dir.canonicalFile
    if (!target.isInside(noBackupFilesDir.canonicalFile)) return false
    return sharedRoots.none { target.isInside(it.canonicalFile) || target == it.canonicalFile }
}

private fun File.isInside(parent: File): Boolean {
    var current: File? = parentFile
    while (current != null) {
        if (current == parent) return true
        current = current.parentFile
    }
    return false
}

const val OFFLINE_DIR_NAME = "offline_copies"
