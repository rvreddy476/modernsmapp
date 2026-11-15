package com.us.android.feature.profile.ui

import androidx.compose.runtime.Immutable
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.feature.profile.data.OfficialMarkSource
import com.us.android.feature.profile.data.OrgApplicationBody
import com.us.android.feature.profile.data.OrgStatus
import com.us.android.feature.profile.data.officialCode
import com.us.android.feature.profile.data.officialMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Which step the Official mark screen draws, from the server's `next`. */
enum class OfficialStep { Apply, EmailCode, Review, Done }

private const val GSTIN_LENGTH = 15
private const val CIN_LENGTH = 21

/** The registration the applicant names: a GSTIN (15 characters) or a CIN (21). */
enum class RegistrationKind(val label: String, val maxLength: Int) {
    GSTIN("GSTIN", GSTIN_LENGTH),
    CIN("CIN", CIN_LENGTH),
}

/** The Official mark screen, as it draws. */
@Immutable
data class OfficialMarkUiState(
    val loading: Boolean = true,
    /** The first read failed for a reason other than "verify as a person first". */
    val loadError: String? = null,
    val status: OrgStatus? = null,
    /** "Start again" on the code step: the form is shown locally until a new submit. */
    val startedAgain: Boolean = false,
    val legalName: String = "",
    val registrationKind: RegistrationKind = RegistrationKind.GSTIN,
    val registration: String = "",
    val domain: String = "",
    val email: String = "",
    val code: String = "",
    val submitting: Boolean = false,
    /** The refusal under the button. */
    val error: String? = null,
    /** PERSON_VERIFICATION_REQUIRED, in the server's words: an error card; the form stays usable. */
    val personRequired: String? = null,
    val inviteOpen: Boolean = false,
    val inviteCode: String = "",
    val accepting: Boolean = false,
    val inviteError: String? = null,
) {
    val step: OfficialStep?
        get() = when {
            status == null -> null
            startedAgain -> OfficialStep.Apply
            else -> stepOf(status.next)
        }

    /** The last application was refused or rejected, and the form is up again. */
    val showRefused: Boolean
        get() = step == OfficialStep.Apply && !startedAgain &&
            (status?.status == STATUS_REFUSED || status?.status == STATUS_REJECTED)

    val canSubmit: Boolean
        get() = !submitting && legalName.isNotBlank() && registration.isNotBlank() &&
            domain.isNotBlank() && email.isNotBlank()

    val canConfirm: Boolean get() = !submitting && code.isNotBlank()

    val canAccept: Boolean get() = !accepting && inviteCode.isNotBlank()

    val isGovernment: Boolean get() = status?.verificationLevel == LEVEL_GOVERNMENT

    companion object {
        const val STATUS_REFUSED = "refused"
        const val STATUS_REJECTED = "rejected"
        const val LEVEL_GOVERNMENT = "government"

        fun stepOf(next: String): OfficialStep = when (next) {
            "apply" -> OfficialStep.Apply
            "email_code" -> OfficialStep.EmailCode
            "" -> OfficialStep.Done
            else -> OfficialStep.Review
        }
    }
}

/**
 * Settings › Account › Request verification › Official mark (2026-11-15): the
 * registry-checked mark for organisations, plus accepting a Government invitation.
 * Every step is the server's `next`; the client keeps only the form's text.
 */
@HiltViewModel
class OfficialMarkViewModel @Inject constructor(
    private val source: OfficialMarkSource,
) : ViewModel() {

    private val _state = MutableStateFlow(OfficialMarkUiState())
    val state: StateFlow<OfficialMarkUiState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        _state.update { it.copy(loading = true, loadError = null) }
        viewModelScope.launch {
            when (val result = source.status()) {
                is AppResult.Success -> _state.update { it.copy(loading = false, status = result.data) }
                is AppResult.Failure -> _state.update {
                    if (result.error.officialCode() == CODE_PERSON_REQUIRED) {
                        it.copy(
                            loading = false,
                            status = it.status ?: OrgStatus(next = "apply"),
                            personRequired = result.error.officialMessage(),
                        )
                    } else {
                        it.copy(loading = false, loadError = result.error.officialMessage())
                    }
                }
            }
        }
    }

    fun onLegalName(value: String) = _state.update { it.copy(legalName = value.take(FIELD_MAX), error = null) }

    fun onRegistrationKind(kind: RegistrationKind) = _state.update {
        it.copy(registrationKind = kind, registration = it.registration.take(kind.maxLength), error = null)
    }

    fun onRegistration(value: String) = _state.update {
        it.copy(registration = value.uppercase().take(it.registrationKind.maxLength), error = null)
    }

    fun onDomain(value: String) = _state.update { it.copy(domain = value.take(FIELD_MAX), error = null) }

    fun onEmail(value: String) = _state.update { it.copy(email = value.take(FIELD_MAX), error = null) }

    fun onCode(value: String) = _state.update {
        it.copy(code = value.filter(Char::isDigit).take(CODE_MAX), error = null)
    }

    fun startAgain() = _state.update { it.copy(startedAgain = true, code = "", error = null) }

    fun submit() {
        val now = _state.value
        if (!now.canSubmit) return
        val registration = now.registration.trim()
        val body = OrgApplicationBody(
            legalName = now.legalName.trim(),
            gstin = registration.takeIf { now.registrationKind == RegistrationKind.GSTIN },
            cin = registration.takeIf { now.registrationKind == RegistrationKind.CIN },
            officialDomain = now.domain.trim(),
            signatoryEmail = now.email.trim(),
        )
        _state.update { it.copy(submitting = true, error = null) }
        viewModelScope.launch {
            when (val result = source.apply(body)) {
                is AppResult.Success -> _state.update {
                    it.copy(
                        submitting = false,
                        status = result.data,
                        startedAgain = false,
                        code = "",
                        personRequired = null,
                    )
                }
                is AppResult.Failure -> _state.update { refused(it.copy(submitting = false), result.error) }
            }
        }
    }

    fun confirm() {
        val now = _state.value
        if (!now.canConfirm) return
        _state.update { it.copy(submitting = true, error = null) }
        viewModelScope.launch {
            when (val result = source.confirmEmail(now.code)) {
                is AppResult.Success -> _state.update {
                    it.copy(submitting = false, status = result.data, code = "")
                }
                is AppResult.Failure -> {
                    _state.update { refused(it.copy(submitting = false), result.error) }
                    // The application is gone or locked: re-read, and the screen falls back to the form.
                    if (result.error.officialCode() in REREAD_AFTER) reread()
                }
            }
        }
    }

    fun openInvite() = _state.update { it.copy(inviteOpen = true) }

    fun onInviteCode(value: String) = _state.update {
        it.copy(inviteCode = value.trim().take(INVITE_MAX), inviteError = null)
    }

    fun accept() {
        val now = _state.value
        if (!now.canAccept) return
        _state.update { it.copy(accepting = true, inviteError = null) }
        viewModelScope.launch {
            when (val result = source.acceptGovernment(now.inviteCode)) {
                is AppResult.Success -> _state.update {
                    it.copy(
                        accepting = false,
                        status = result.data,
                        startedAgain = false,
                        inviteOpen = false,
                        inviteCode = "",
                    )
                }
                is AppResult.Failure -> _state.update {
                    it.copy(accepting = false, inviteError = result.error.officialMessage())
                }
            }
        }
    }

    private suspend fun reread() {
        val result = source.status()
        if (result is AppResult.Success) _state.update { it.copy(status = result.data, code = "") }
    }

    private fun refused(state: OfficialMarkUiState, error: AppError): OfficialMarkUiState =
        if (error.officialCode() == CODE_PERSON_REQUIRED) {
            state.copy(personRequired = error.officialMessage())
        } else {
            state.copy(error = error.officialMessage())
        }

    private companion object {
        const val CODE_PERSON_REQUIRED = "PERSON_VERIFICATION_REQUIRED"
        val REREAD_AFTER = setOf("NO_PENDING_APPLICATION", "TOO_MANY_CODE_ATTEMPTS")
        const val FIELD_MAX = 200
        const val CODE_MAX = 16
        const val INVITE_MAX = 64
    }
}

/**
 * Whether Request verification offers the Official mark row: only when the first read of
 * the status answered 200. A 503 `ORG_VERIFICATION_DISABLED`, or any other failure, hides it.
 */
@HiltViewModel
class OfficialMarkEntryViewModel @Inject constructor(
    private val source: OfficialMarkSource,
) : ViewModel() {

    private val _shown = MutableStateFlow(false)
    val shown: StateFlow<Boolean> = _shown.asStateFlow()

    init {
        viewModelScope.launch { _shown.value = source.status() is AppResult.Success }
    }
}
