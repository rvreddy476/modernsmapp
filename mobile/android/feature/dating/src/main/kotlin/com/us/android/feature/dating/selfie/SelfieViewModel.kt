package com.us.android.feature.dating.selfie

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.feature.dating.ConsentGate
import com.us.android.feature.dating.ConsentType
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.network.SelfieChallengeDto
import com.us.android.feature.dating.network.SelfieResultDto
import com.us.android.feature.dating.network.VerificationStatusDto
import com.us.android.feature.dating.photos.UploadOutcome
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.io.File
import javax.inject.Inject

/** Where the blink-twice selfie check is. */
sealed interface SelfieState {
    data object Loading : SelfieState

    /** The biometric consent has not been given. No challenge has been requested. */
    data object NeedsConsent : SelfieState

    /** The person said no. Verification cannot continue without it. */
    data object ConsentDeclined : SelfieState

    /** A live challenge: record a clip of at most [recordMillis] while blinking twice. */
    data class Ready(
        val challengeId: String,
        val recordMillis: Long,
        val note: String? = null,
        /** From `GET /verification/status`, not inferred from the last verdict. */
        val attemptsLeft: Int? = null,
    ) : SelfieState

    data class Uploading(val progress: Float) : SelfieState

    data object Checking : SelfieState

    /**
     * The clip is uploaded but media-service has not finished with it
     * (409 MEDIA_NOT_READY). The server refuses BEFORE spending an attempt, and
     * the challenge is still live, so the SAME clip can simply be sent again.
     */
    data class StillProcessing(
        val challengeId: String,
        val mediaId: String,
        val recordMillis: Long,
        val attemptsLeft: Int? = null,
    ) : SelfieState

    data object Passed : SelfieState

    /** Borderline: a moderator decides. */
    data object InReview : SelfieState

    /** The check did not pass and can be tried again with a NEW challenge. */
    data class Retry(val reason: String?, val copy: String, val attemptsRemaining: Int?) : SelfieState

    /** Five attempts in 24 hours are used up. */
    data object LimitReached : SelfieState

    /** Something on the profile must change first (the main photo is not approved). */
    data class Blocked(val copy: String) : SelfieState

    /** A failure that is not a verdict — network, the compare service. */
    data class Error(val copy: String) : SelfieState
}

/**
 * The selfie liveness check (D5): consent → challenge (`blink_twice`) → a
 * front-camera clip of at most 4 s → upload through `:core:media` →
 * `POST /verification/selfie {challenge_id, video_media_id}` → the verdict.
 *
 * A challenge is single use: every retry asks for a fresh one.
 */
@HiltViewModel
class SelfieViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
    private val uploader: SelfieVideoUploader,
) : ViewModel() {

    private val _state = MutableStateFlow<SelfieState>(SelfieState.Loading)
    val state: StateFlow<SelfieState> = _state.asStateFlow()

    init {
        start()
    }

    /** Attempts left today, from the server. Null until the status has been read. */
    private var attemptsLeft: Int? = null

    fun start() {
        if (ConsentGate.selfieNeedsConsent(session.consents.value)) {
            _state.value = SelfieState.NeedsConsent
            return
        }
        _state.value = SelfieState.Loading
        viewModelScope.launch {
            // The SERVER says where the check stands — passed, in review, out of
            // attempts — rather than the app inferring it from the last verdict
            // this process happened to see.
            val status = (repository.verificationStatus() as? DatingResult.Success)?.value
            attemptsLeft = status?.selfie?.attemptsLeftToday
            val settled = status?.let(SelfieOutcomes::fromStatus)
            if (settled != null) {
                _state.value = settled
                return@launch
            }
            requestChallenge()
        }
    }

    fun onConsentAnswered(granted: Boolean) {
        if (_state.value != SelfieState.NeedsConsent && _state.value != SelfieState.ConsentDeclined) return
        if (!granted) {
            _state.value = SelfieState.ConsentDeclined
            return
        }
        _state.value = SelfieState.Loading
        viewModelScope.launch {
            when (val result = repository.setConsent(ConsentType.BIOMETRIC_SELFIE.wire, granted = true)) {
                is DatingResult.Success -> {
                    session.setConsents(result.value)
                    requestChallenge()
                }
                is DatingResult.Failure -> _state.value = SelfieState.Error(DatingCopy.forError(result.error))
            }
        }
    }

    /** After a verdict or an error: a new attempt on a fresh challenge. */
    fun retry() {
        when (_state.value) {
            is SelfieState.Retry, is SelfieState.Error -> requestChallenge()
            SelfieState.ConsentDeclined -> _state.value = SelfieState.NeedsConsent
            else -> Unit
        }
    }

    /**
     * Sends the clip that is already uploaded again, after MEDIA_NOT_READY.
     * No new challenge and no new recording: the attempt was never spent.
     */
    fun resubmit() {
        val waiting = _state.value as? SelfieState.StillProcessing ?: return
        _state.value = SelfieState.Checking
        viewModelScope.launch { submit(waiting.challengeId, waiting.mediaId, waiting.recordMillis) }
    }

    /** The camera finished a clip. */
    fun onRecorded(file: File) {
        val ready = _state.value as? SelfieState.Ready ?: return
        _state.value = SelfieState.Uploading(0f)
        viewModelScope.launch {
            val mediaId = when (val outcome = uploader.upload(file) { p -> _state.value = SelfieState.Uploading(p) }) {
                is UploadOutcome.Ready -> outcome.mediaId
                is UploadOutcome.Failed -> {
                    // Nothing was submitted, so the challenge is still unused.
                    _state.value = ready.copy(note = outcome.message)
                    return@launch
                }
            }
            _state.value = SelfieState.Checking
            submit(ready.challengeId, mediaId, ready.recordMillis)
        }
    }

    private suspend fun submit(challengeId: String, mediaId: String, recordMillis: Long) {
        when (val result = repository.submitSelfie(challengeId, mediaId)) {
            is DatingResult.Success -> {
                _state.value = SelfieOutcomes.fromResult(result.value)
                attemptsLeft = result.value.attemptsRemaining
            }
            is DatingResult.Failure ->
                if (result.error.code == SelfieOutcomes.CODE_MEDIA_NOT_READY) {
                    // Refused before the attempt was spent: keep the challenge
                    // and the clip, and let them send it again in a moment.
                    _state.value = SelfieState.StillProcessing(challengeId, mediaId, recordMillis, attemptsLeft)
                } else {
                    _state.value = submitFailure(result.error) ?: return
                }
        }
    }

    fun onRecordingFailed() {
        val ready = _state.value as? SelfieState.Ready ?: return
        _state.value = ready.copy(note = "The recording didn't work. Try again.")
    }

    private fun requestChallenge() {
        _state.value = SelfieState.Loading
        viewModelScope.launch {
            _state.value = when (val result = repository.selfieChallenge()) {
                is DatingResult.Success -> ready(result.value)
                is DatingResult.Failure -> challengeFailure(result.error)
            }
        }
    }

    private fun challengeFailure(error: DatingError): SelfieState = when {
        error is DatingError.ConsentRequired -> SelfieState.NeedsConsent
        else -> SelfieOutcomes.forRefusal(error)
    }

    /** Null when the failure was handled by requesting a new challenge. */
    private fun submitFailure(error: DatingError): SelfieState? = when {
        error is DatingError.ConsentRequired -> SelfieState.NeedsConsent
        error.code == "SELFIE_CHALLENGE_INVALID" || error.code == "SELFIE_CHALLENGE_REQUIRED" -> {
            requestChallenge()
            null
        }
        else -> SelfieOutcomes.forRefusal(error)
    }

    private fun ready(challenge: SelfieChallengeDto): SelfieState.Ready = SelfieState.Ready(
        challengeId = challenge.challengeId,
        recordMillis = SelfieOutcomes.recordMillis(challenge.maxDurationMs),
        attemptsLeft = attemptsLeft,
    )
}

/** The verdicts and refusals in the words the person reads. Pure. */
object SelfieOutcomes {

    /** The server's cap (`max_duration_ms`, 4000) less a margin for the encoder, never more than 4 s. */
    fun recordMillis(maxDurationMs: Int): Long {
        val cap = if (maxDurationMs in 1..MAX_CLIP_MS) maxDurationMs else MAX_CLIP_MS
        return (cap - ENCODER_MARGIN_MS).coerceAtLeast(MIN_CLIP_MS).toLong()
    }

    /**
     * What `GET /verification/status` settles on its own, or null when the
     * person should go on and record a clip.
     *
     * `next_step` is the server's word for what happens next, so it decides;
     * the selfie state is read only for the two terminal verdicts.
     */
    fun fromStatus(status: VerificationStatusDto): SelfieState? = when {
        status.selfie.state == STATE_PASSED -> SelfieState.Passed
        status.selfie.state == STATE_REVIEW || status.nextStep == NEXT_WAIT_FOR_REVIEW -> SelfieState.InReview
        status.nextStep == NEXT_RETRY_TOMORROW -> SelfieState.LimitReached
        // A status with attempts but no next step to take is not a verdict:
        // fall through and ask for a challenge.
        else -> null
    }

    fun fromResult(result: SelfieResultDto): SelfieState = when {
        result.passed || result.status == "passed" -> SelfieState.Passed
        result.status == "pending_review" -> SelfieState.InReview
        result.attemptsRemaining <= 0 && result.status == "failed" -> SelfieState.LimitReached
        else -> SelfieState.Retry(result.reason, copyFor(result.reason), result.attemptsRemaining)
    }

    @Suppress("CyclomaticComplexMethod")
    fun forRefusal(error: DatingError): SelfieState = when (error.code) {
        "SELFIE_ATTEMPTS_EXCEEDED" -> SelfieState.LimitReached
        "SELFIE_ALREADY_PASSED" -> SelfieState.Passed
        "SELFIE_REVIEW_PENDING" -> SelfieState.InReview
        "PRIMARY_PHOTO_NOT_APPROVED" -> SelfieState.Blocked("Your main photo needs to be approved before the face check.")
        "SELFIE_VIDEO_TOO_LONG" -> SelfieState.Retry("VIDEO_TOO_LONG", "Keep the clip under 4 seconds, then try again.", null)
        "SELFIE_SAME_AS_PRIMARY_PHOTO" -> SelfieState.Retry("SAME_AS_PRIMARY", "Record a new selfie video — not your profile photo.", null)
        "SELFIE_MEDIA_NOT_FOUND", "SELFIE_VIDEO_UNSUPPORTED" -> SelfieState.Retry("VIDEO_UNUSABLE", "That recording couldn't be used. Record again.", null)
        "FACE_COMPARE_UNAVAILABLE" -> SelfieState.Error("Verification is unavailable right now. Try again in a few minutes.")
        else -> SelfieState.Error(DatingCopy.forError(error))
    }

    fun copyFor(reason: String?): String = when (reason) {
        "NOT_ENOUGH_BLINKS" -> "We couldn't see you blink twice. Hold the phone at eye level, keep your face in the " +
            "frame, and blink twice slowly while it records."
        "NO_FACE" -> "We couldn't see your face. Face the camera in good light and try again."
        "MULTIPLE_FACES" -> "More than one face was in the video. Make sure only you are in the frame."
        "FACE_CHANGED" -> "Keep your face in the frame for the whole clip, then try again."
        "LOW_QUALITY" -> "The video was too dark or blurry. Find better light and hold the phone steady."
        "NO_MATCH" -> "The video didn't match your main photo. Make sure your main photo clearly shows your face."
        else -> "The check didn't pass. Try again."
    }

    fun attemptsLine(remaining: Int?): String? = when {
        remaining == null -> null
        remaining == 1 -> "1 attempt left today"
        remaining > 1 -> "$remaining attempts left today"
        else -> null
    }

    /** 409 from `POST /verification/selfie`: the clip is still being processed. */
    const val CODE_MEDIA_NOT_READY = "MEDIA_NOT_READY"

    const val MEDIA_NOT_READY_COPY = "Your video is still processing. Try again in a moment."

    const val MAX_CLIP_MS = 4_000

    private const val STATE_PASSED = "passed"
    private const val STATE_REVIEW = "review"
    private const val NEXT_WAIT_FOR_REVIEW = "wait_for_review"
    private const val NEXT_RETRY_TOMORROW = "retry_tomorrow"

    /**
     * How long after the limit the recorder's own watchdog stops the clip, for
     * the case where CameraX's duration cap does not fire. [recordMillis] plus
     * this must still leave the clip inside [MAX_CLIP_MS] — asserted in the tests.
     */
    const val RECORD_WATCHDOG_MS = 200L

    private const val ENCODER_MARGIN_MS = 300
    private const val MIN_CLIP_MS = 2_000
}
