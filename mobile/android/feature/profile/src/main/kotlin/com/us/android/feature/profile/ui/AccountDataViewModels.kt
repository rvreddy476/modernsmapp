package com.us.android.feature.profile.ui

import android.content.ContentResolver
import android.net.Uri
import androidx.compose.runtime.Immutable
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.datastore.DataSaverStore
import com.us.android.core.profile.data.AccountDataSource
import com.us.android.core.profile.data.VerificationKind
import com.us.android.core.profile.data.verificationRefusal
import dagger.hilt.android.lifecycle.HiltViewModel
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.OutputStream
import javax.inject.Inject

/** Where Download your data stands. */
@Immutable
data class DataExportUiState(
    val exporting: Boolean = false,
    /** "Saved", or why not; shown under the row, then dismissed. */
    val message: String? = null,
    val failed: Boolean = false,
)

/** Opens where the export is written; a seam so the ViewModel tests without a ContentResolver. */
fun interface ExportTarget {
    fun open(uri: Uri): OutputStream?
}

/**
 * Settings' Data & storage rows (2026-10-24): the Data saver switch, stored on
 * this device and read by the media layer, and Download your data — the
 * account's export streamed into the file the viewer picked with the
 * system's own "Save to" (Storage Access Framework), never a path of ours.
 */
@HiltViewModel
class AccountDataViewModel @Inject constructor(
    private val store: DataSaverStore,
    private val source: AccountDataSource,
    private val target: ExportTarget,
    /** Where the file is opened; the tests hand in their own. */
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : ViewModel() {

    val dataSaver: StateFlow<Boolean> = store.enabled
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), false)

    fun setDataSaver(enabled: Boolean) {
        viewModelScope.launch { store.setEnabled(enabled) }
    }

    private val _export = MutableStateFlow(DataExportUiState())
    val export: StateFlow<DataExportUiState> = _export.asStateFlow()

    /** The viewer picked where to save it ([uri]); null is "they backed out", and nothing happens. */
    fun exportTo(uri: Uri?) = exportWith(uri?.let { picked -> { target.open(picked) } })

    /** [exportTo] with the file already resolved to an opener; null is "backed out". */
    internal fun exportWith(open: (() -> OutputStream?)?) {
        if (open == null || _export.value.exporting) return
        _export.value = DataExportUiState(exporting = true)
        viewModelScope.launch {
            val stream = withContext(io) { runCatching { open() }.getOrNull() }
            if (stream == null) {
                _export.value = DataExportUiState(message = EXPORT_NO_FILE, failed = true)
                return@launch
            }
            val result = stream.use { source.exportTo(it) }
            _export.value = when (result) {
                is AppResult.Success -> DataExportUiState(message = EXPORT_SAVED)
                is AppResult.Failure -> DataExportUiState(message = exportRefusal(result.error), failed = true)
            }
        }
    }

    fun dismissExportMessage() = _export.update { it.copy(message = null) }

    companion object {
        const val EXPORT_SAVED = "Your data was saved."
        const val EXPORT_NO_FILE = "That file couldn't be opened. Pick another place."
        private const val STOP_TIMEOUT_MILLIS = 5_000L

        internal fun exportRefusal(error: AppError): String = when (error) {
            is AppError.NoNetwork, is AppError.Timeout -> "You're offline. Try again when you're connected."
            is AppError.RateLimited -> "Too many requests. Wait a moment and try again."
            else -> "Your data couldn't be downloaded. Try again."
        }
    }
}

/** The production [ExportTarget]: the picked document, opened for writing. */
class ContentResolverExportTarget @Inject constructor(
    @dagger.hilt.android.qualifiers.ApplicationContext private val context: android.content.Context,
) : ExportTarget {
    override fun open(uri: Uri): OutputStream? {
        val resolver: ContentResolver = context.contentResolver
        return resolver.openOutputStream(uri, "wt")
    }
}

/** The verification page, as it draws. */
@Immutable
data class VerificationUiState(
    val kind: VerificationKind? = null,
    val note: String = "",
    val submitting: Boolean = false,
    val error: String? = null,
    /** Sent: the page says it is waiting for review. */
    val submitted: Boolean = false,
) {
    val canSubmit: Boolean get() = kind != null && !submitting && !submitted
}

/**
 * Request verification (2026-10-24, the web's /settings/verification): pick
 * one of the four kinds trust-safety takes, an optional note, send. There is
 * no self-service status read upstream, so a second request is refused by the
 * server ("a pending verification request already exists") and said as such.
 */
@HiltViewModel
class VerificationViewModel @Inject constructor(
    private val source: AccountDataSource,
) : ViewModel() {

    private val _state = MutableStateFlow(VerificationUiState())
    val state: StateFlow<VerificationUiState> = _state.asStateFlow()

    fun select(kind: VerificationKind) = _state.update { it.copy(kind = kind, error = null) }

    fun onNoteChange(note: String) = _state.update { it.copy(note = note.take(NOTE_MAX), error = null) }

    fun submit() {
        val now = _state.value
        val kind = now.kind ?: return
        if (!now.canSubmit) return
        _state.update { it.copy(submitting = true, error = null) }
        viewModelScope.launch {
            val result = source.requestVerification(kind, now.note)
            _state.update {
                when (result) {
                    is AppResult.Success -> it.copy(submitting = false, submitted = true)
                    is AppResult.Failure -> it.copy(submitting = false, error = verificationRefusal(result.error))
                }
            }
        }
    }

    private companion object {
        const val NOTE_MAX = 500
    }
}
