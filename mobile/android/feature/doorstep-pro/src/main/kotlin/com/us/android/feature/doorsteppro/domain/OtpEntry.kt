package com.us.android.feature.doorsteppro.domain

import com.us.android.feature.doorsteppro.data.ProCodes
import com.us.android.feature.doorsteppro.data.ProError
import com.us.android.feature.doorsteppro.data.code
import com.us.android.feature.doorsteppro.data.detailInt
import com.us.android.feature.doorsteppro.data.detailText
import com.us.android.feature.doorsteppro.ui.IST
import com.us.android.feature.doorsteppro.ui.instantOrNull
import java.time.Duration
import java.time.Instant
import java.time.format.DateTimeFormatter
import java.util.Locale

/** Which code the professional is asking the customer for. */
enum class OtpKind(val label: String) {
    START("start code"),
    END("end code"),
}

/**
 * One OTP entry box: what is typed, what the server last said about it.
 *
 * The customer reads a 4-digit code (OTPInput `^[0-9]{4}$`) to the
 * professional. A wrong code costs an attempt (422 DOORSTEP_OTP_INVALID,
 * `details.attempts_left`); too many lock entry (423 DOORSTEP_OTP_LOCKED,
 * `details.locked_until`). The server owns both counts; this only shows them.
 */
data class OtpEntry(
    val kind: OtpKind,
    val code: String = "",
    /** From the last refusal; null until the server has said. */
    val attemptsLeft: Int? = null,
    val lockedUntil: Instant? = null,
    /** Locked without a readable time: entry stays shut until a reload says otherwise. */
    val lockedIndefinitely: Boolean = false,
    val wrongCode: Boolean = false,
)

object OtpRules {
    const val LENGTH = 4

    /** Digits only, at most four — paste-safe. */
    fun sanitize(input: String): String = input.filter { it in '0'..'9' }.take(LENGTH)

    fun isLocked(entry: OtpEntry, now: Instant): Boolean =
        entry.lockedIndefinitely || entry.lockedUntil?.isAfter(now) == true

    fun canSubmit(entry: OtpEntry, now: Instant): Boolean = entry.code.length == LENGTH && !isLocked(entry, now)

    fun onTyped(entry: OtpEntry, input: String): OtpEntry = entry.copy(code = sanitize(input), wrongCode = false)

    /**
     * Applies a refusal of the start / complete call. Returns null when the
     * refusal is not about the code (photos missing, wrong status, …) — the
     * caller shows that one itself.
     */
    fun onRefused(entry: OtpEntry, error: ProError): OtpEntry? = when (error.code) {
        ProCodes.OTP_INVALID -> entry.copy(code = "", wrongCode = true, attemptsLeft = error.detailInt("attempts_left"))
        ProCodes.OTP_LOCKED -> {
            val until = instantOrNull(error.detailText("locked_until"))
            entry.copy(code = "", wrongCode = false, attemptsLeft = 0, lockedUntil = until, lockedIndefinitely = until == null)
        }
        else -> null
    }

    /** The line under the box, or null when there is nothing to say. */
    fun message(entry: OtpEntry, now: Instant): String? {
        if (isLocked(entry, now)) {
            val until = entry.lockedUntil
            if (until == null || entry.lockedIndefinitely) {
                return "Too many wrong codes. Entry is locked for now — try again later or call support."
            }
            val minutes = (Duration.between(now, until).toMillis() + MILLIS_PER_MINUTE - 1) / MILLIS_PER_MINUTE
            val at = CLOCK.format(until.atZone(IST))
            return "Too many wrong codes. Try again at $at (in $minutes min)."
        }
        if (!entry.wrongCode) return null
        return when (val left = entry.attemptsLeft) {
            null -> "That ${entry.kind.label} is wrong. Ask the customer to read it again."
            0 -> "That ${entry.kind.label} is wrong. The next wrong code locks entry."
            1 -> "That ${entry.kind.label} is wrong. 1 attempt left before entry locks."
            else -> "That ${entry.kind.label} is wrong. $left attempts left."
        }
    }

    private const val MILLIS_PER_MINUTE = 60_000L
    private val CLOCK: DateTimeFormatter = DateTimeFormatter.ofPattern("h:mm a", Locale.ENGLISH)
}
