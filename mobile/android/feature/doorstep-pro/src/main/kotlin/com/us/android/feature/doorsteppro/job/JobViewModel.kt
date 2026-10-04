package com.us.android.feature.doorsteppro.job

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.doorsteppro.camera.PickedPhoto
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ExtraDto
import com.us.android.feature.doorsteppro.data.ExtraOptionDto
import com.us.android.feature.doorsteppro.data.ExtraRequest
import com.us.android.feature.doorsteppro.data.LocationRequest
import com.us.android.feature.doorsteppro.data.ProCodes
import com.us.android.feature.doorsteppro.data.ProError
import com.us.android.feature.doorsteppro.data.ProJobDto
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.SosRequest
import com.us.android.feature.doorsteppro.data.code
import com.us.android.feature.doorsteppro.data.detailInt
import com.us.android.feature.doorsteppro.data.userMessage
import com.us.android.feature.doorsteppro.domain.JobStatus
import com.us.android.feature.doorsteppro.domain.OtpEntry
import com.us.android.feature.doorsteppro.domain.OtpKind
import com.us.android.feature.doorsteppro.domain.OtpRules
import com.us.android.feature.doorsteppro.domain.PhotoCounts
import com.us.android.feature.doorsteppro.domain.PhotoGate
import com.us.android.feature.doorsteppro.domain.PhotoPhase
import com.us.android.feature.doorsteppro.domain.ProClock
import com.us.android.feature.doorsteppro.domain.VisitActions
import com.us.android.feature.doorsteppro.domain.VisitFlow
import com.us.android.feature.doorsteppro.location.CurrentLocationSource
import com.us.android.feature.doorsteppro.location.ProDuty
import com.us.android.feature.doorsteppro.navigation.requireArg
import com.us.android.feature.doorsteppro.store.VisitMemory
import com.us.android.feature.doorsteppro.ui.distanceText
import com.us.android.feature.doorsteppro.ui.errorMessage
import com.us.android.feature.doorsteppro.ui.successMessage
import com.us.android.feature.doorsteppro.ui.warningMessage
import com.us.android.feature.doorsteppro.upload.PhotoUploads
import com.us.android.feature.doorsteppro.upload.UploadOutcome
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.time.Instant
import javax.inject.Inject

/** An extra being put together in the proposal dialog. */
data class ExtraDraft(
    val option: ExtraOptionDto? = null,
    val quantity: Int = 1,
    val evidenceMediaId: String? = null,
    val uploadingEvidence: Boolean = false,
)

/** The confirmations the job screen can be showing. */
enum class JobDialog { CANCEL, UNSAFE_EXIT, SOS, NO_SHOW }

data class JobUiState(
    val loading: Boolean = true,
    val job: ProJobDto? = null,
    val error: String? = null,
    val now: Instant = Instant.EPOCH,
    val arrivedAt: Instant? = null,
    val finished: Boolean = false,
    val photos: PhotoCounts = PhotoCounts(),
    /** The phase the next captured photo is for. */
    val pendingPhase: PhotoPhase? = null,
    val uploadingPhase: PhotoPhase? = null,
    val uploadProgress: Float = 0f,
    val startOtp: OtpEntry = OtpEntry(OtpKind.START),
    val endOtp: OtpEntry = OtpEntry(OtpKind.END),
    val extras: List<ExtraDto> = emptyList(),
    /** null until loaded; empty when the job allows none. */
    val extraOptions: List<ExtraOptionDto>? = null,
    /** The rate-card route is not on the server yet (PROPOSED) or failed. */
    val extraOptionsUnavailable: Boolean = false,
    val extraDraft: ExtraDraft? = null,
    val dialog: JobDialog? = null,
    val busy: Boolean = false,
    /** The job is no longer this professional's (given back, unsafe exit): the screen closes. */
    val released: Boolean = false,
    val ratingStars: Int = 0,
    val ratingTags: Set<String> = emptySet(),
    val ratingComment: String = "",
    val rated: Boolean = false,
    val message: UsMessage? = null,
) {
    val status: JobStatus get() = JobStatus.of(job?.status)

    val actions: VisitActions? get() = job?.let { VisitFlow.of(it, now, arrivedAt, finished) }

    val missingToStart: Map<PhotoPhase, Int> get() = job?.let { PhotoGate.missingToStart(it.photosRequired, photos) }.orEmpty()

    val missingToComplete: Map<PhotoPhase, Int> get() = job?.let { PhotoGate.missingToComplete(it.photosRequired, photos) }.orEmpty()

    /** Extras still waiting for the customer's decision: finishing is refused while any are. */
    val undecidedExtras: Int get() = extras.count { it.status == PROPOSED }

    companion object {
        const val PROPOSED = "proposed"
        val RATING_TAGS = listOf("Polite", "Ready on time", "Clear instructions", "Not ready", "Rude", "Felt unsafe")
    }
}

/**
 * One job, the visit flow end to end (x-doorstep-booking-states, professional
 * side): en route → arrived (geo-checked) → before photos + the customer's
 * start code → extras from the rate card → work done → after photos + the
 * end code → completed → rate the customer; customer no-show after the wait,
 * give the job back before the start code, "unsafe, leaving", SOS.
 *
 * The server decides every transition and the screen re-reads the job after
 * each; OTP lockout and the photo gate are shown from the server's own
 * refusals ([OtpRules], [PhotoGate]). The job is re-read every 20 s while it
 * is active, so a cancellation or an approved extra shows up without a tap.
 */
@HiltViewModel
@Suppress("TooManyFunctions")
class JobViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DoorstepProRepository,
    private val uploads: PhotoUploads,
    private val location: CurrentLocationSource,
    private val memory: VisitMemory,
    private val duty: ProDuty,
    private val clock: ProClock,
) : ViewModel() {

    private val bookingId = savedStateHandle.requireArg("bookingId")

    private val _state = MutableStateFlow(
        JobUiState(now = clock.now(), arrivedAt = memory.arrivedAt(bookingId), finished = memory.finished(bookingId)),
    )
    val state: StateFlow<JobUiState> = _state.asStateFlow()

    init {
        refresh()
        loadExtras()
        viewModelScope.launch {
            var sincePoll = 0L
            while (isActive) {
                delay(TICK_MILLIS)
                sincePoll += TICK_MILLIS
                _state.update { it.copy(now = clock.now()) }
                if (sincePoll >= POLL_MILLIS && _state.value.status.isActive && !_state.value.busy) {
                    sincePoll = 0L
                    refresh()
                }
            }
        }
    }

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.job(bookingId)) {
                is ProResult.Success -> applyJob(result.value)
                is ProResult.Failure -> _state.update {
                    it.copy(loading = false, error = if (it.job == null) result.error.userMessage() else null)
                }
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun showDialog(dialog: JobDialog?) = _state.update { it.copy(dialog = dialog) }

    // ── Travel and arrival ──

    fun goEnRoute() = act { repository.enRoute(bookingId) }

    fun markArrived() {
        if (_state.value.busy) return
        _state.update { it.copy(busy = true) }
        viewModelScope.launch {
            val fix = location.current()
            if (fix == null) {
                _state.update { it.copy(busy = false, message = errorMessage("Couldn't get your location. Turn on GPS and try again.")) }
                return@launch
            }
            when (val result = repository.arrived(bookingId, LocationRequest(lat = fix.latitude, lng = fix.longitude))) {
                is ProResult.Success -> {
                    val at = clock.now()
                    memory.markArrived(bookingId, at)
                    _state.update { it.copy(arrivedAt = at) }
                    applyJob(result.value)
                }
                is ProResult.Failure -> _state.update { it.copy(busy = false, message = errorMessage(arrivedRefusal(result.error))) }
            }
        }
    }

    // ── Photos ──

    /** The screen opens the camera next; the photo it brings back is for [phase]. */
    fun willCapture(phase: PhotoPhase) = _state.update { it.copy(pendingPhase = phase) }

    fun onPhoto(photo: PickedPhoto) {
        val phase = _state.value.pendingPhase ?: return
        _state.update { it.copy(pendingPhase = null, uploadingPhase = phase, uploadProgress = 0f) }
        viewModelScope.launch {
            val mediaId = when (val outcome = uploads.uploadImage(photo.uri) { p -> _state.update { it.copy(uploadProgress = p) } }) {
                is UploadOutcome.Ready -> outcome.mediaId
                is UploadOutcome.Failed -> {
                    _state.update { it.copy(uploadingPhase = null, message = errorMessage(outcome.message)) }
                    return@launch
                }
            }
            if (phase == PhotoPhase.EXTRA_EVIDENCE) _state.update { s -> s.copy(extraDraft = s.extraDraft?.copy(uploadingEvidence = true)) }
            val fix = if (location.hasPermission()) location.current() else null
            when (val result = repository.recordPhoto(bookingId, phase.wire, mediaId, fix?.latitude, fix?.longitude)) {
                is ProResult.Success -> _state.update { s ->
                    s.copy(
                        uploadingPhase = null,
                        photos = s.photos.plus(phase),
                        extraDraft = if (phase == PhotoPhase.EXTRA_EVIDENCE) {
                            s.extraDraft?.copy(evidenceMediaId = mediaId, uploadingEvidence = false)
                        } else {
                            s.extraDraft
                        },
                    )
                }
                is ProResult.Failure -> _state.update { s ->
                    s.copy(uploadingPhase = null, extraDraft = s.extraDraft?.copy(uploadingEvidence = false), message = errorMessage(result.error.userMessage()))
                }
            }
        }
    }

    // ── Start and complete with the customer's codes ──

    fun onStartOtp(input: String) = _state.update { it.copy(startOtp = OtpRules.onTyped(it.startOtp, input)) }

    fun onEndOtp(input: String) = _state.update { it.copy(endOtp = OtpRules.onTyped(it.endOtp, input)) }

    fun submitStart() {
        val s = _state.value
        PhotoGate.missingText(s.missingToStart)?.let { text ->
            _state.update { it.copy(message = errorMessage(text)) }
            return
        }
        if (!OtpRules.canSubmit(s.startOtp, clock.now()) || s.busy) return
        val code = s.startOtp.code
        otpCall(OtpKind.START) { repository.start(bookingId, code) }
    }

    fun submitComplete() {
        val s = _state.value
        PhotoGate.missingText(s.missingToComplete)?.let { text ->
            _state.update { it.copy(message = errorMessage(text)) }
            return
        }
        if (!OtpRules.canSubmit(s.endOtp, clock.now()) || s.busy) return
        val code = s.endOtp.code
        otpCall(OtpKind.END) { repository.complete(bookingId, code) }
    }

    private fun otpCall(kind: OtpKind, call: suspend () -> ProResult<ProJobDto>) {
        _state.update { it.copy(busy = true) }
        viewModelScope.launch {
            when (val result = call()) {
                is ProResult.Success -> {
                    _state.update {
                        if (kind == OtpKind.START) it.copy(startOtp = OtpEntry(OtpKind.START)) else it.copy(endOtp = OtpEntry(OtpKind.END))
                    }
                    applyJob(result.value)
                    if (kind == OtpKind.END) _state.update { it.copy(message = successMessage("Job complete. Well done.")) }
                }
                is ProResult.Failure -> _state.update { s ->
                    val entry = if (kind == OtpKind.START) s.startOtp else s.endOtp
                    val refused = OtpRules.onRefused(entry, result.error)
                    val photos = PhotoGate.reconcile(s.photos, result.error)
                    val next = s.copy(busy = false, photos = photos)
                    when {
                        refused != null -> if (kind == OtpKind.START) next.copy(startOtp = refused) else next.copy(endOtp = refused)
                        result.error.code == ProCodes.PHOTOS_REQUIRED -> next.copy(
                            message = errorMessage(
                                PhotoGate.missingText(
                                    if (kind == OtpKind.START) {
                                        PhotoGate.missingToStart(s.job!!.photosRequired, photos)
                                    } else {
                                        PhotoGate.missingToComplete(s.job!!.photosRequired, photos)
                                    },
                                ) ?: result.error.userMessage(),
                            ),
                        )
                        else -> next.copy(message = errorMessage(result.error.userMessage()))
                    }
                }
            }
        }
    }

    // ── Extras ──

    fun loadExtras() {
        viewModelScope.launch {
            (repository.extras(bookingId) as? ProResult.Success)?.value?.let { extras -> _state.update { it.copy(extras = extras) } }
            when (val options = repository.extraOptions(bookingId)) {
                is ProResult.Success -> _state.update { it.copy(extraOptions = options.value, extraOptionsUnavailable = false) }
                is ProResult.Failure -> _state.update { it.copy(extraOptionsUnavailable = true) }
            }
        }
    }

    fun openExtraDraft() = _state.update { it.copy(extraDraft = ExtraDraft()) }

    fun closeExtraDraft() = _state.update { it.copy(extraDraft = null) }

    fun pickExtraOption(option: ExtraOptionDto) = _state.update { s -> s.copy(extraDraft = s.extraDraft?.copy(option = option, quantity = 1)) }

    fun setExtraQuantity(quantity: Int) = _state.update { s ->
        val draft = s.extraDraft ?: return@update s
        val max = draft.option?.maxQuantity?.coerceAtLeast(1) ?: 1
        s.copy(extraDraft = draft.copy(quantity = quantity.coerceIn(1, max)))
    }

    fun proposeExtra() {
        val draft = _state.value.extraDraft ?: return
        val option = draft.option ?: return
        if (_state.value.busy) return
        _state.update { it.copy(busy = true) }
        val request = ExtraRequest(
            quantity = draft.quantity,
            rateCardId = option.rateCardId.takeIf { option.kind == RATE_CARD },
            addonId = option.addonId.takeIf { option.kind == ADDON },
            evidenceMediaId = draft.evidenceMediaId,
        )
        viewModelScope.launch {
            when (val result = repository.proposeExtra(bookingId, request)) {
                is ProResult.Success -> _state.update {
                    it.copy(
                        busy = false,
                        extraDraft = null,
                        extras = it.extras + result.value,
                        message = successMessage("Sent to the customer to approve."),
                    )
                }
                is ProResult.Failure -> _state.update { it.copy(busy = false, message = errorMessage(result.error.userMessage())) }
            }
        }
    }

    fun withdrawExtra(extraId: String) {
        viewModelScope.launch {
            when (val result = repository.withdrawExtra(bookingId, extraId)) {
                is ProResult.Success -> _state.update { s ->
                    s.copy(extras = s.extras.map { if (it.id == extraId) it.copy(status = WITHDRAWN) else it })
                }
                is ProResult.Failure -> _state.update { it.copy(message = errorMessage(result.error.userMessage())) }
            }
        }
    }

    // ── Work done ──

    fun finish() {
        if (_state.value.undecidedExtras > 0) {
            _state.update { it.copy(message = errorMessage("Wait for the customer to approve or decline your extras, or withdraw them.")) }
            return
        }
        if (_state.value.busy) return
        _state.update { it.copy(busy = true) }
        viewModelScope.launch {
            when (val result = repository.finish(bookingId)) {
                is ProResult.Success -> {
                    memory.markFinished(bookingId)
                    _state.update { it.copy(finished = true) }
                    applyJob(result.value)
                }
                is ProResult.Failure -> {
                    if (result.error.code == ProCodes.EXTRAS_PENDING) loadExtras()
                    _state.update { it.copy(busy = false, message = errorMessage(result.error.userMessage())) }
                }
            }
        }
    }

    // ── Off-ramps ──

    fun customerNoShow() {
        showDialog(null)
        act { repository.customerNoShow(bookingId) }
    }

    fun giveBack(reason: String) {
        if (_state.value.busy) return
        _state.update { it.copy(busy = true, dialog = null) }
        viewModelScope.launch {
            when (val result = repository.cancel(bookingId, reason.ifBlank { DEFAULT_CANCEL_REASON })) {
                is ProResult.Success -> {
                    duty.setTravelling(false)
                    _state.update { it.copy(busy = false, released = true) }
                }
                is ProResult.Failure -> _state.update { it.copy(busy = false, message = errorMessage(result.error.userMessage())) }
            }
        }
    }

    fun sos(note: String) = incident(note, unsafeExit = false)

    fun unsafeExit(note: String) = incident(note, unsafeExit = true)

    private fun incident(note: String, unsafeExit: Boolean) {
        _state.update { it.copy(dialog = null, busy = true) }
        viewModelScope.launch {
            val fix = if (location.hasPermission()) location.current() else null
            val request = SosRequest(lat = fix?.latitude, lng = fix?.longitude, note = note.trim().ifBlank { null })
            val result = if (unsafeExit) repository.unsafeExit(bookingId, request) else repository.sos(bookingId, request)
            when (result) {
                is ProResult.Success -> if (unsafeExit) {
                    duty.setTravelling(false)
                    _state.update {
                        it.copy(busy = false, released = true, message = warningMessage("You've left the job. Doorstep's safety team has been alerted."))
                    }
                } else {
                    _state.update { it.copy(busy = false, message = warningMessage("SOS sent. Doorstep's safety team has been alerted and will call you.")) }
                }
                is ProResult.Failure -> _state.update {
                    it.copy(busy = false, message = errorMessage("SOS didn't go through: ${result.error.userMessage()} Call 112 if you are in danger."))
                }
            }
        }
    }

    // ── Rating ──

    fun setStars(stars: Int) = _state.update { it.copy(ratingStars = stars.coerceIn(0, MAX_STARS)) }

    fun toggleTag(tag: String) = _state.update { it.copy(ratingTags = if (tag in it.ratingTags) it.ratingTags - tag else it.ratingTags + tag) }

    fun setComment(text: String) = _state.update { it.copy(ratingComment = text.take(MAX_COMMENT)) }

    fun submitRating() {
        val s = _state.value
        if (s.ratingStars !in 1..MAX_STARS || s.busy) return
        _state.update { it.copy(busy = true) }
        viewModelScope.launch {
            when (val result = repository.rateCustomer(bookingId, s.ratingStars, s.ratingTags.toList(), s.ratingComment)) {
                is ProResult.Success -> _state.update { it.copy(busy = false, rated = true) }
                is ProResult.Failure -> if (result.error.code == ProCodes.RATING_EXISTS) {
                    _state.update { it.copy(busy = false, rated = true) }
                } else {
                    _state.update { it.copy(busy = false, message = errorMessage(result.error.userMessage())) }
                }
            }
        }
    }

    private fun act(call: suspend () -> ProResult<ProJobDto>) {
        if (_state.value.busy) return
        _state.update { it.copy(busy = true) }
        viewModelScope.launch {
            when (val result = call()) {
                is ProResult.Success -> applyJob(result.value)
                is ProResult.Failure -> _state.update { it.copy(busy = false, message = errorMessage(result.error.userMessage())) }
            }
        }
    }

    private fun applyJob(job: ProJobDto) {
        duty.setTravelling(JobStatus.of(job.status) == JobStatus.EN_ROUTE)
        _state.update { it.copy(loading = false, busy = false, job = job, error = null) }
    }

    private fun arrivedRefusal(error: ProError): String {
        if (error.code != ProCodes.GEO_CHECK_FAILED) return error.userMessage()
        val distance = error.detailInt("distance_m")
        val max = error.detailInt("max_distance_m")
        return if (distance != null && max != null) {
            "You're ${distanceText(distance)} from the address. Get within ${distanceText(max)} and try again."
        } else {
            "You're not at the customer's address yet."
        }
    }

    private companion object {
        const val TICK_MILLIS = 1_000L
        const val POLL_MILLIS = 20_000L
        const val MAX_STARS = 5
        const val MAX_COMMENT = 1_000
        const val RATE_CARD = "rate_card"
        const val ADDON = "addon"
        const val WITHDRAWN = "withdrawn"
        const val DEFAULT_CANCEL_REASON = "Professional gave the job back"
    }
}
