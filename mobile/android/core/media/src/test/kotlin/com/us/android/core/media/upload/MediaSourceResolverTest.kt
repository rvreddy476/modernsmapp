package com.us.android.core.media.upload

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.database.MatrixCursor
import android.net.Uri
import android.provider.OpenableColumns
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * A lapsed Photo Picker grant (audit 2026-10-30): after a process death the
 * picker's provider THROWS on `getType` and `query`. The resolver answers
 * "can't be read" (null) instead of crashing the upload worker, and a
 * readable pick still resolves.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class MediaSourceResolverTest {

    /** The picker's provider once this process no longer holds the grant. */
    class RevokedProvider : ContentProvider() {
        override fun onCreate() = true
        override fun getType(uri: Uri): String = throw SecurityException("Permission Denial: no grant")
        override fun query(uri: Uri, p: Array<out String>?, s: String?, a: Array<out String>?, o: String?): Cursor =
            throw SecurityException("Permission Denial: no grant")
        override fun insert(uri: Uri, values: ContentValues?): Uri? = null
        override fun delete(uri: Uri, s: String?, a: Array<out String>?) = 0
        override fun update(uri: Uri, v: ContentValues?, s: String?, a: Array<out String>?) = 0
    }

    /** A provider that only refuses the size lookup. */
    class QueryRevokedProvider : ContentProvider() {
        override fun onCreate() = true
        override fun getType(uri: Uri) = "video/mp4"
        override fun query(uri: Uri, p: Array<out String>?, s: String?, a: Array<out String>?, o: String?): Cursor =
            throw SecurityException("Permission Denial: no grant")
        override fun insert(uri: Uri, values: ContentValues?): Uri? = null
        override fun delete(uri: Uri, s: String?, a: Array<out String>?) = 0
        override fun update(uri: Uri, v: ContentValues?, s: String?, a: Array<out String>?) = 0
    }

    /** A readable pick of 1,234 bytes. */
    class ReadableProvider : ContentProvider() {
        override fun onCreate() = true
        override fun getType(uri: Uri) = "video/mp4"
        override fun query(uri: Uri, p: Array<out String>?, s: String?, a: Array<out String>?, o: String?): Cursor =
            MatrixCursor(arrayOf(OpenableColumns.SIZE)).apply { addRow(arrayOf<Any>(1_234L)) }
        override fun insert(uri: Uri, values: ContentValues?): Uri? = null
        override fun delete(uri: Uri, s: String?, a: Array<out String>?) = 0
        override fun update(uri: Uri, v: ContentValues?, s: String?, a: Array<out String>?) = 0
    }

    private val resolver = AndroidMediaSourceResolver(ApplicationProvider.getApplicationContext())

    @Test
    fun `a revoked grant is unreadable, not a crash`() {
        Robolectric.setupContentProvider(RevokedProvider::class.java, "test.picker.revoked")

        assertThat(resolver.resolve("content://test.picker.revoked/media/1")).isNull()
    }

    @Test
    fun `a size lookup refused after the type is unreadable too`() {
        Robolectric.setupContentProvider(QueryRevokedProvider::class.java, "test.picker.query")

        assertThat(resolver.resolve("content://test.picker.query/media/1")).isNull()
    }

    @Test
    fun `a readable pick resolves with its type and size`() {
        Robolectric.setupContentProvider(ReadableProvider::class.java, "test.picker.ok")

        val picked = resolver.resolve("content://test.picker.ok/media/1")

        assertThat(picked?.mimeType).isEqualTo("video/mp4")
        assertThat(picked?.sizeBytes).isEqualTo(1_234L)
    }
}
