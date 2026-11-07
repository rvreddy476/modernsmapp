package com.us.android.core.media.upload

import android.content.ContentResolver
import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import java.io.IOException
import javax.inject.Inject
import javax.inject.Singleton

/** A picked local asset whose stream can be reopened for an upload retry. */
data class PickedMedia(
    val uri: String,
    val mimeType: String,
    val sizeBytes: Long,
    val source: UploadSource,
)

/** Product-neutral bridge between Android's URI grants and the upload core. */
fun interface MediaSourceResolver {
    fun resolve(uri: String): PickedMedia?
}

@Singleton
class AndroidMediaSourceResolver @Inject constructor(
    @ApplicationContext context: Context,
) : MediaSourceResolver {
    private val resolver: ContentResolver = context.contentResolver

    /**
     * Null when the media cannot be looked up, a lapsed grant included. A
     * Photo Picker URI is a grant to the process that picked it: after a
     * process death `getType`/`query` THROW `SecurityException` rather than
     * answering null (audit 2026-10-30), and the upload worker crashed on it
     * at every launch. "Can't be read" is the answer either way.
     */
    override fun resolve(uri: String): PickedMedia? {
        val parsed = runCatching { Uri.parse(uri) }.getOrNull() ?: return null
        val mimeType = lookup { resolver.getType(parsed) } ?: return null
        val sizeBytes = lookup { querySize(parsed) } ?: return null
        if (sizeBytes <= 0L) return null

        return PickedMedia(
            uri = uri,
            mimeType = mimeType,
            sizeBytes = sizeBytes,
            source = UploadSource {
                resolver.openInputStream(parsed)
                    ?: throw IOException("Cannot open selected media")
            },
        )
    }

    /**
     * A provider's refusal, or its crash, as null. A provider may throw
     * `SecurityException`, `IllegalArgumentException` (an unknown URI) or
     * `IllegalStateException` (a closed cursor), and nothing here can act
     * on the difference.
     */
    private inline fun <T> lookup(block: () -> T?): T? = runCatching(block).getOrNull()

    private fun querySize(uri: Uri): Long? =
        resolver.query(uri, arrayOf(OpenableColumns.SIZE), null, null, null)?.use { cursor ->
            if (!cursor.moveToFirst()) return@use null
            val column = cursor.getColumnIndex(OpenableColumns.SIZE)
            if (column < 0 || cursor.isNull(column)) null else cursor.getLong(column)
        }
}

@Module
@InstallIn(SingletonComponent::class)
abstract class MediaSourceModule {
    @Binds
    @Singleton
    abstract fun bindMediaSourceResolver(
        implementation: AndroidMediaSourceResolver,
    ): MediaSourceResolver
}
