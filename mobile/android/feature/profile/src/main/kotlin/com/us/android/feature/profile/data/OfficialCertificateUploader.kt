package com.us.android.feature.profile.data

import android.content.ContentResolver
import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.FILE_TYPE_DOCUMENT
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.UploadSource
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.withContext
import java.io.IOException
import javax.inject.Inject

/**
 * The Official mark's optional registration certificate (contract
 * docs/designs/official-marks-console-contract.md §6, 16 Nov 2026): PDF, JPEG or PNG,
 * at most 10 MB, checked here before any byte moves and again by media-service.
 */
val CERTIFICATE_MIME_TYPES: List<String> = listOf("application/pdf", "image/jpeg", "image/png")

/** 10 MB, media-service's cap for the private `document` kind. */
const val CERTIFICATE_MAX_BYTES: Long = 10_485_760L

/** A file the applicant picked with the system document picker; [source] reopens it per attempt. */
data class PickedCertificate(
    val name: String,
    val mimeType: String,
    val sizeBytes: Long,
    val source: UploadSource,
)

/** Resolves a picker URI; null when the grant lapsed or the provider would not answer. */
fun interface CertificateReader {
    suspend fun read(uri: String): PickedCertificate?
}

/** Uploads a checked certificate; the media id, or null when any step failed. A port for JVM tests. */
fun interface OfficialCertificateUploader {
    suspend fun upload(certificate: PickedCertificate): String?
}

/** Name, type and size through the ContentResolver, off the main thread. */
class ContentCertificateReader @Inject constructor(
    @ApplicationContext context: Context,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : CertificateReader {
    private val resolver: ContentResolver = context.contentResolver

    override suspend fun read(uri: String): PickedCertificate? = withContext(io) {
        runCatching {
            val parsed = Uri.parse(uri)
            val mime = resolver.getType(parsed) ?: return@runCatching null
            var name: String? = null
            var size: Long? = null
            resolver.query(parsed, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)
                ?.use { cursor ->
                    if (cursor.moveToFirst()) {
                        val n = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                        val s = cursor.getColumnIndex(OpenableColumns.SIZE)
                        if (n >= 0 && !cursor.isNull(n)) name = cursor.getString(n)
                        if (s >= 0 && !cursor.isNull(s)) size = cursor.getLong(s)
                    }
                }
            val bytes = size?.takeIf { it > 0L } ?: return@runCatching null
            PickedCertificate(
                name = name?.takeIf { it.isNotBlank() } ?: DEFAULT_NAME,
                mimeType = mime,
                sizeBytes = bytes,
                source = UploadSource {
                    resolver.openInputStream(parsed) ?: throw IOException("Cannot open the certificate")
                },
            )
        }.getOrNull()
    }

    private companion object {
        const val DEFAULT_NAME = "Certificate"
    }
}

/**
 * Through `:core:media`'s [MediaUploader], the same three steps the person selfie uses,
 * with media-service's private `document` kind: `POST /v1/media/init`
 * (`file_type: document`) → presigned PUT → `POST /v1/media/confirm`. No lease: the
 * certificate is held for review by profile-service, never reclaimed as an abandoned draft.
 */
class MediaOfficialCertificateUploader @Inject constructor(
    private val uploader: MediaUploader,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : OfficialCertificateUploader {

    override suspend fun upload(certificate: PickedCertificate): String? = withContext(io) {
        val reserved = when (
            val result = uploader.reserve(
                mimeType = certificate.mimeType,
                sizeBytes = certificate.sizeBytes,
                uploadPurpose = NO_LEASE,
                fileType = FILE_TYPE_DOCUMENT,
            )
        ) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> return@withContext null
        }
        if (reserved.mediaId.isBlank()) return@withContext null
        val put = uploader.upload(
            reserved.uploadUrl,
            certificate.mimeType,
            certificate.sizeBytes,
            certificate.source,
        ) { _, _ -> }
        if (put != PresignedPutResult.Success) return@withContext null
        if (uploader.confirm(reserved.mediaId) !is AppResult.Success) return@withContext null
        reserved.mediaId
    }

    private companion object {
        const val NO_LEASE = ""
    }
}
