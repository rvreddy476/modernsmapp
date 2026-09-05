package com.us.android.feature.post.createhub.studio

import android.content.Context
import android.net.Uri
import android.os.Handler
import android.os.Looper
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.util.UnstableApi
import androidx.media3.transformer.Composition
import androidx.media3.transformer.EditedMediaItem
import androidx.media3.transformer.Effects
import androidx.media3.transformer.ExportException
import androidx.media3.transformer.ExportResult
import androidx.media3.transformer.ProgressHolder
import androidx.media3.transformer.Transformer
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.CancellableContinuation
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import java.io.File
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.coroutines.resume

/** How an export ended. */
sealed interface ExportOutcome {
    data class Done(val path: String, val durationMs: Long) : ExportOutcome
    data class Failed(val message: String) : ExportOutcome
    data object Cancelled : ExportOutcome
}

/**
 * Renders a [ReelEdit] to a file. A port so the studio's ViewModel is
 * testable on the JVM; the Android implementation is Media3's Transformer.
 */
interface ReelExporter {
    /**
     * Writes the edited reel to [target], reporting 0..100 through
     * [onProgress]. Cancelling the calling coroutine cancels the export
     * and answers [ExportOutcome.Cancelled].
     */
    suspend fun export(edit: ReelEdit, target: File, onProgress: (Int) -> Unit): ExportOutcome
}

/**
 * Media3 Transformer (2026-09-05): H.264 in MP4, the trim as the item's
 * clipping, the speed as its [androidx.media3.common.audio.SpeedProvider]
 * (audio kept, stretched with the video), and [ReelEffects] for the rest.
 * Output is capped at 1080 × 1920 by the [androidx.media3.effect.Presentation]
 * inside those effects.
 *
 * Transformer wants a Looper: it is built and driven on the main thread,
 * and its progress is polled there four times a second — cheap, and the
 * sheet only shows whole percents.
 */
@UnstableApi
@Singleton
class TransformerReelExporter @Inject constructor(
    @ApplicationContext private val context: Context,
    private val pills: TextPillRenderer,
) : ReelExporter {

    override suspend fun export(edit: ReelEdit, target: File, onProgress: (Int) -> Unit): ExportOutcome =
        withContext(Dispatchers.Main.immediate) {
            target.parentFile?.mkdirs()
            target.delete()
            val transformer = Transformer.Builder(context)
                .setVideoMimeType(MimeTypes.VIDEO_H264)
                .setAudioMimeType(MimeTypes.AUDIO_AAC)
                .build()
            val outcome = suspendCancellableCoroutine<ExportOutcome> { continuation ->
                transformer.addListener(
                    object : Transformer.Listener {
                        override fun onCompleted(composition: Composition, exportResult: ExportResult) {
                            if (continuation.isActive) {
                                continuation.resume(ExportOutcome.Done(target.absolutePath, exportResult.durationMs))
                            }
                        }

                        override fun onError(
                            composition: Composition,
                            exportResult: ExportResult,
                            exportException: ExportException,
                        ) {
                            if (continuation.isActive) {
                                continuation.resume(ExportOutcome.Failed(message(exportException)))
                            }
                        }
                    },
                )
                continuation.invokeOnCancellation { runCatching { transformer.cancel() } }
                transformer.start(editedItem(edit), target.absolutePath)
                poll(transformer, onProgress, continuation)
            }
            if (outcome !is ExportOutcome.Done) target.delete()
            outcome
        }

    /** Reports progress until the export settles; runs on the same Looper as the transformer. */
    private fun poll(
        transformer: Transformer,
        onProgress: (Int) -> Unit,
        continuation: CancellableContinuation<ExportOutcome>,
    ) {
        val holder = ProgressHolder()
        val handler = Handler(Looper.getMainLooper())
        var last = -1
        val tick = object : Runnable {
            override fun run() {
                if (!continuation.isActive) return
                val state = runCatching { transformer.getProgress(holder) }
                    .getOrDefault(Transformer.PROGRESS_STATE_NOT_STARTED)
                if (state == Transformer.PROGRESS_STATE_AVAILABLE && holder.progress != last) {
                    last = holder.progress
                    onProgress(holder.progress.coerceIn(0, PERCENT))
                }
                handler.postDelayed(this, POLL_MILLIS)
            }
        }
        handler.post(tick)
    }

    private fun editedItem(edit: ReelEdit): EditedMediaItem {
        val mediaItem = MediaItem.Builder()
            .setUri(Uri.parse(edit.sourceUri))
            .setClippingConfiguration(
                MediaItem.ClippingConfiguration.Builder()
                    .setStartPositionUs(edit.trimStartUs)
                    .setEndPositionUs(edit.trimEndUs)
                    .build(),
            )
            .build()
        val size = ReelFrame.outputSize(edit.width, edit.height)
        val effects = ReelEffects.build(edit) { pill -> pills.render(pill, size.width) }
        return EditedMediaItem.Builder(mediaItem)
            .setEffects(Effects(emptyList(), effects))
            .setSpeed(ReelEffects.speedProvider(edit.speed))
            .build()
    }

    private fun message(error: ExportException): String = when (error.errorCode) {
        ExportException.ERROR_CODE_DECODING_FORMAT_UNSUPPORTED,
        ExportException.ERROR_CODE_DECODER_INIT_FAILED,
        -> "This video's format can't be edited on this phone."
        ExportException.ERROR_CODE_ENCODER_INIT_FAILED,
        ExportException.ERROR_CODE_ENCODING_FORMAT_UNSUPPORTED,
        -> "This phone can't encode the reel. Try a shorter clip."
        ExportException.ERROR_CODE_IO_FILE_NOT_FOUND,
        ExportException.ERROR_CODE_IO_NO_PERMISSION,
        -> "That video can't be read. Pick it again."
        else -> "Couldn't prepare the reel. Try again."
    }

    private companion object {
        const val POLL_MILLIS = 250L
        const val PERCENT = 100
    }
}

@Module
@InstallIn(SingletonComponent::class)
abstract class ReelStudioModule {
    @OptIn(UnstableApi::class)
    @Binds
    @Singleton
    abstract fun bindExporter(implementation: TransformerReelExporter): ReelExporter
}
