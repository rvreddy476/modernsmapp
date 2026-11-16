package com.us.android.feature.profile.ui

import androidx.compose.runtime.Immutable
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.feature.profile.data.PersonReturn
import com.us.android.feature.profile.data.PersonReturnLinks
import com.us.android.feature.profile.data.PersonSelfieUploader
import com.us.android.feature.profile.data.PersonStatus
import com.us.android.feature.profile.data.PersonVerificationReturns
import com.us.android.feature.profile.data.PersonVerificationSource
import com.us.android.feature.profile.data.SelfieUpload
import com.us.android.feature.profile.data.officialCode
import com.us.android.feature.profile.data.officialMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.io.File
import javax.inject.Inject

/** Which step the person-verification screen draws, from the server's `next`. */
enum class PersonStep { DigiLocker, Selfie, Done }

/** The person-verification screen, as it draws. */
@Immutable
data class PersonVerificationUiState(
    val loading: Boolean = true,
    /** The first read failed. */
    val loadError: String? = null,
    val status: PersonStatus? = null,
    /** Starting DigiLocker, or posting its return. */
    val starting: Boolean = false,
    /** Uploading the selfie and waiting for the server's check: "Checking…". */
    val checking: Boolean = false,
    /** The refusal under the action, in the server's words. */
    val error: String? = null,
) {
    val step: PersonStep? get() = status?.let { stepOf(it.next) }

    /** The last attempt was refused and DigiLocker is offered again. */
    val showRefused: Boolean
        get() = step == PersonStep.DigiLocker && status?.attempt?.status == ATTEMPT_REFUSED

    /** The account already holds the Official or Government mark: no "Apply" on the done state. */
    val holdsHigherMark: Boolean
        get() = status?.verificationLevel in HIGHER_LEVELS

    companion object {
        const val ATTEMPT_REFUSED = "refused"
        private val HIGHER_LEVELS = setOf("organisation", "government")

        fun stepOf(next: String): PersonStep = when (next) {
            "" -> PersonStep.Done
            "selfie" -> PersonStep.Selfie
            else -> PersonStep.DigiLocker
        }
    }
}

/**
 * Request verification › Verify you're a real person (2026-11-15): Aadhaar through
 * DigiLocker, then a short front-camera selfie matched against the Aadhaar photo.
 * Every step is the server's `next`; the client holds nothing but the in-flight clip.
 */
@HiltViewModel
class PersonVerificationViewModel @Inject constructor(
    private val source: PersonVerificationSource,
    private val uploader: PersonSelfieUploader,
    private val returns: PersonVerificationReturns,
) : ViewModel() {

    private val _state = MutableStateFlow(PersonVerificationUiState())
    val state: StateFlow<PersonVerificationUiState> = _state.asStateFlow()

    private val _open = MutableSharedFlow<String>(
        extraBufferCapacity = 1,
        onBufferOverflow = BufferOverflow.DROP_OLDEST,
    )

    /** A DigiLocker authorize URL for the screen to open in the browser. */
    val open: SharedFlow<String> = _open.asSharedFlow()

    init {
        load()
        viewModelScope.launch {
            returns.pending.filterNotNull().collect {
                returns.take()?.let { link -> postReturn(link) }
            }
        }
    }

    fun load() {
        _state.update { it.copy(loading = true, loadError = null) }
        viewModelScope.launch {
            when (val result = source.status()) {
                is AppResult.Success -> _state.update { it.copy(loading = false, status = result.data) }
                is AppResult.Failure -> _state.update {
                    it.copy(loading = false, loadError = result.error.officialMessage())
                }
            }
        }
    }

    /**
     * Back in view on the done state (16 Nov 2026): re-read, so the page stops offering
     * "Apply for the Official mark" once the mark is held. Mid-flow steps are left alone.
     */
    fun refreshIfDone() {
        if (_state.value.step == PersonStep.Done) load()
    }

    /** "Continue with DigiLocker". */
    fun start() {
        val now = _state.value
        if (now.starting || now.checking) return
        _state.update { it.copy(starting = true, error = null) }
        viewModelScope.launch {
            when (val result = source.start()) {
                is AppResult.Success -> {
                    val url = result.data.authorizeUrl
                    val mock = PersonReturnLinks.mockReturnOf(url)
                    when {
                        mock != null -> postReturn(mock)
                        isBrowserUrl(url) -> {
                            _state.update { it.copy(starting = false) }
                            _open.tryEmit(url)
                        }
                        else -> _state.update { it.copy(starting = false, error = FALLBACK_LINK) }
                    }
                }
                is AppResult.Failure -> _state.update {
                    it.copy(starting = false, error = result.error.officialMessage())
                }
            }
        }
    }

    fun onBrowserMissing() = _state.update { it.copy(starting = false, error = NO_BROWSER) }

    /** The camera finished: [file] is the clip, or null when nothing usable was written. */
    fun onSelfieRecorded(file: File?) {
        if (file == null) {
            _state.update { it.copy(error = NOT_RECORDED) }
            return
        }
        if (_state.value.checking) {
            file.delete()
            return
        }
        _state.update { it.copy(checking = true, error = null) }
        viewModelScope.launch {
            when (val upload = uploader.upload(file)) {
                is SelfieUpload.Failed -> _state.update { it.copy(checking = false, error = upload.message) }
                is SelfieUpload.Ready -> settle(source.selfie(upload.mediaId)) { copy(checking = false) }
            }
        }
    }

    /** The camera could not start, or the person refused it. */
    fun onCameraProblem(message: String) = _state.update { it.copy(error = message) }

    private suspend fun postReturn(link: PersonReturn) {
        _state.update { it.copy(starting = true, error = null) }
        settle(source.callback(state = link.state, code = link.code)) { copy(starting = false) }
    }

    private suspend fun settle(
        result: AppResult<PersonStatus>,
        idle: PersonVerificationUiState.() -> PersonVerificationUiState,
    ) {
        when (result) {
            is AppResult.Success -> _state.update { it.idle().copy(status = result.data, loading = false) }
            is AppResult.Failure -> {
                _state.update { it.idle().copy(error = result.error.officialMessage()) }
                // The attempt is gone or refused: the server is back at "digilocker".
                if (result.error.officialCode() in REREAD_AFTER) reread()
            }
        }
    }

    private suspend fun reread() {
        val result = source.status()
        if (result is AppResult.Success) _state.update { it.copy(status = result.data) }
    }

    private fun isBrowserUrl(url: String): Boolean =
        url.startsWith("https://", ignoreCase = true) || url.startsWith("http://", ignoreCase = true)

    private companion object {
        val REREAD_AFTER = setOf("NO_PENDING_ATTEMPT", "INVALID_STATE", "LIVENESS_FAILED", "FACE_MISMATCH")
        const val NO_BROWSER = "No browser on this phone can open DigiLocker."
        const val FALLBACK_LINK = "Something went wrong. Try again."
        const val NOT_RECORDED = "The recording didn't save. Try again."
    }
}

/** What Request verification shows for the person row. */
@Immutable
data class PersonEntryState(val shown: Boolean = false, val verified: Boolean = false)

/**
 * Whether Request verification offers the "Verify you're a real person" row: only when the
 * first read of the status answered 200. A 503 `PERSON_VERIFICATION_DISABLED`, or any other
 * failure, hides it. On the done state (`next` = "") the row's second line reads "Verified".
 */
@HiltViewModel
class PersonVerificationEntryViewModel @Inject constructor(
    private val source: PersonVerificationSource,
) : ViewModel() {

    private val _state = MutableStateFlow(PersonEntryState())
    val state: StateFlow<PersonEntryState> = _state.asStateFlow()

    /** Reads the status; Request verification calls it each time it comes into view. */
    fun refresh() {
        viewModelScope.launch {
            _state.value = when (val result = source.status()) {
                is AppResult.Success -> PersonEntryState(shown = true, verified = result.data.next.isEmpty())
                is AppResult.Failure -> PersonEntryState()
            }
        }
    }
}
