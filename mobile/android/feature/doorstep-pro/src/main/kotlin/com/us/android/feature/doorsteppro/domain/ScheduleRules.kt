package com.us.android.feature.doorsteppro.domain

import com.us.android.feature.doorsteppro.data.HoursWindowDto

/** One working window being edited: weekday 0 = Sunday, minutes after midnight in India time. */
data class HoursWindow(val weekday: Int, val startMinutes: Int, val endMinutes: Int) {
    fun toDto(): HoursWindowDto = HoursWindowDto(weekday = weekday, start = HoursRules.hhmm(startMinutes), end = HoursRules.hhmm(endMinutes))
}

/** A problem with one window, by its index in the list as it will be sent. */
data class HoursProblem(val index: Int, val message: String)

/**
 * The weekly-hours editor's rules — the server's prokyc.ValidateHours,
 * mirrored so a professional fixes an overlap before saving, not after:
 * weekday 0–6, `HH:MM` times, end after start on the same day (no window
 * crosses midnight), at most six windows a day, no two windows of a day
 * overlapping (touching is fine). The server re-validates; its
 * `items[i]: …` message wins when it disagrees.
 */
object HoursRules {
    const val MAX_WINDOWS_PER_DAY = 6
    const val STEP_MINUTES = 30
    private const val MINUTES_PER_DAY = 24 * 60
    private const val MINUTES_PER_HOUR = 60
    private const val DEFAULT_START = 9 * 60
    private const val DEFAULT_END = 18 * 60
    private const val DEFAULT_LENGTH = 4 * 60
    private val HHMM = Regex("^([01][0-9]|2[0-3]):[0-5][0-9]$")

    fun hhmm(minutes: Int): String {
        val m = minutes.coerceIn(0, MINUTES_PER_DAY - 1)
        return "${(m / MINUTES_PER_HOUR).toString().padStart(2, '0')}:${(m % MINUTES_PER_HOUR).toString().padStart(2, '0')}"
    }

    /** `HH:MM` → minutes; null when malformed (24:00 included — the server refuses it too). */
    fun minutes(text: String): Int? {
        if (!HHMM.matches(text)) return null
        return text.substring(0, 2).toInt() * MINUTES_PER_HOUR + text.substring(3, 5).toInt()
    }

    fun fromDto(dto: HoursWindowDto): HoursWindow? {
        val start = minutes(dto.start) ?: return null
        val end = minutes(dto.end) ?: return null
        return HoursWindow(dto.weekday, start, end)
    }

    /** The windows as they will be sent: by weekday, then start. */
    fun sorted(windows: List<HoursWindow>): List<HoursWindow> = windows.sortedWith(compareBy({ it.weekday }, { it.startMinutes }))

    /** The first problem, or null when the server should accept the list. */
    fun validate(windows: List<HoursWindow>): HoursProblem? {
        val perDay = mutableMapOf<Int, Int>()
        windows.forEachIndexed { i, w ->
            if (w.weekday !in 0..WEEKDAY_MAX) return HoursProblem(i, "Pick a day of the week")
            if (w.startMinutes !in 0 until MINUTES_PER_DAY || w.endMinutes !in 0 until MINUTES_PER_DAY) {
                return HoursProblem(i, "Times must be within the day")
            }
            if (w.endMinutes <= w.startMinutes) return HoursProblem(i, "End must be after start on the same day")
            val count = (perDay[w.weekday] ?: 0) + 1
            if (count > MAX_WINDOWS_PER_DAY) return HoursProblem(i, "At most $MAX_WINDOWS_PER_DAY windows a day")
            perDay[w.weekday] = count
        }
        windows.withIndex().groupBy { it.value.weekday }.values.forEach { day ->
            val ordered = day.sortedBy { it.value.startMinutes }
            ordered.zipWithNext().forEach { (a, b) ->
                if (b.value.startMinutes < a.value.endMinutes) return HoursProblem(b.index, "Windows on one day may not overlap")
            }
        }
        return null
    }

    /** A new window for [weekday]: 9 to 6 on an empty day, else four hours after the day's last window (clamped). */
    fun newWindow(existing: List<HoursWindow>, weekday: Int): HoursWindow? {
        val day = existing.filter { it.weekday == weekday }
        if (day.size >= MAX_WINDOWS_PER_DAY) return null
        val lastEnd = day.maxOfOrNull { it.endMinutes } ?: return HoursWindow(weekday, DEFAULT_START, DEFAULT_END)
        if (lastEnd >= MINUTES_PER_DAY - STEP_MINUTES) return null
        val end = (lastEnd + DEFAULT_LENGTH).coerceAtMost(MINUTES_PER_DAY - STEP_MINUTES)
        return HoursWindow(weekday, lastEnd, end)
    }

    /** The choices a time picker offers: every half hour, 00:00 to 23:30. */
    val choices: List<Int> = (0 until MINUTES_PER_DAY step STEP_MINUTES).toList()

    /** "9:00 AM" for minutes after midnight. */
    fun label(minutes: Int): String {
        val h24 = minutes / MINUTES_PER_HOUR
        val m = minutes % MINUTES_PER_HOUR
        val suffix = if (h24 < NOON) "AM" else "PM"
        val h12 = when (val h = h24 % NOON) {
            0 -> NOON
            else -> h
        }
        return "$h12:${m.toString().padStart(2, '0')} $suffix"
    }

    private const val WEEKDAY_MAX = 6
    private const val NOON = 12
}

/** The service area's radius: 1 to 15 km (doorstep-service MaxServiceRadiusM). */
object AreaRules {
    const val MIN_KM = 1
    const val MAX_KM = 15
    const val DEFAULT_KM = 5
    private const val METRES_PER_KM = 1_000

    fun clampKm(km: Int): Int = km.coerceIn(MIN_KM, MAX_KM)

    fun radiusMeters(km: Int): Int = clampKm(km) * METRES_PER_KM

    fun kmOf(meters: Int): Int = clampKm((meters + METRES_PER_KM / 2) / METRES_PER_KM)
}

/** Client checks mirroring shared/kyc; the server re-validates every one. */
object KycFormats {
    private val IFSC = Regex("^[A-Z]{4}0[A-Z0-9]{6}$")

    /** An individual's PAN: five letters (the fourth P), four digits, a letter. */
    private val PAN = Regex("^[A-Z]{3}P[A-Z][0-9]{4}[A-Z]$")
    private const val MIN_ACCOUNT_DIGITS = 9
    private const val MAX_ACCOUNT_DIGITS = 18

    fun ifsc(input: String): String? = input.trim().uppercase().takeIf { IFSC.matches(it) }

    fun accountNumber(input: String): String? {
        val n = input.trim()
        if (n.length !in MIN_ACCOUNT_DIGITS..MAX_ACCOUNT_DIGITS) return null
        return n.takeIf { value -> value.all { it in '0'..'9' } }
    }

    fun pan(input: String): String? = input.trim().uppercase().takeIf { PAN.matches(it) }

    const val IFSC_MESSAGE = "An IFSC is four letters, a zero and six letters or digits"
    const val ACCOUNT_MESSAGE = "An account number is 9 to 18 digits"
    const val PAN_MESSAGE = "Enter your own PAN: 5 letters (the 4th is P), 4 digits, 1 letter"
}

/** The professional agreement (prokyc.AgreementVersion). Bump with the server's constant and the text below. */
object ProAgreement {
    const val VERSION = "2026-10-04"

    val POINTS: List<String> = listOf(
        "You work as an independent professional. Doorstep connects you with customers; it is not your employer.",
        "Prices are fixed by Doorstep and shown to the customer before booking. Extras come only from the rate card, " +
            "need the customer's approval in their app, and are never taken in cash.",
        "You verify the customer's start code before you begin and the end code when you finish, and take the " +
            "before and after photos the job asks for — of the work area and your kit only, never of the customer.",
        "You keep the customer's address, phone and home private, and use the in-app chat only for the job.",
        "If you feel unsafe you may leave with \"Unsafe, leaving\" at any time without penalty, and use SOS in an emergency.",
        "Your police clearance certificate must stay valid. Doorstep may pause your account while a complaint is reviewed.",
        "Earnings are computed per job after Doorstep's commission and shown in the app.",
    )
}
