package com.us.android.feature.kitchen.hours

import com.us.android.core.food.network.OperatingHoursRequest
import com.us.android.core.food.network.OperatingWindowRequest

/** One day of the week as the partner edits it. [dayOfWeek] is the wire value: 0 = Sunday … 6 = Saturday. */
data class DayHours(
    val dayOfWeek: Int,
    val open: Boolean,
    val opensAt: String,
    val closesAt: String,
)

/**
 * The opening-hours form's pure rules. Times are HH:MM in India Standard Time;
 * a closing time earlier than the opening time is an overnight window, which
 * food-service accepts (18:00 → 02:00).
 */
object OperatingHoursRules {

    /** Monday first on screen. */
    val displayOrder: List<Int> = listOf(1, 2, 3, 4, 5, 6, 0)

    /** The error key for a week with no open day. */
    const val ALL_CLOSED = -1

    private val names = listOf("Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday")
    private val HHMM = Regex("^([01][0-9]|2[0-3]):[0-5][0-9]$")
    private val H_MM = Regex("^[0-9]:[0-5][0-9]$")
    private val SERVER_WINDOW = Regex("^windows\\[(\\d+)]")

    fun dayName(dayOfWeek: Int): String = names[dayOfWeek]

    /** ROUTE GAP: saved hours cannot be read back, so the form starts from 09:00–22:00 every day. */
    fun defaultWeek(): List<DayHours> = (0..6).map { DayHours(it, open = true, opensAt = "09:00", closesAt = "22:00") }

    /** `9:30` → `09:30`, `0930` → `09:30`; anything else is returned trimmed, for [errorFor] to judge. */
    fun normalizeTime(input: String): String {
        val text = input.trim()
        return when {
            HHMM.matches(text) -> text
            H_MM.matches(text) -> "0$text"
            text.length == 4 && text.all(Char::isDigit) -> "${text.take(2)}:${text.drop(2)}"
            else -> text
        }
    }

    fun errorFor(day: DayHours): String? = when {
        !day.open -> null
        !HHMM.matches(day.opensAt) -> "Enter an opening time like 09:00"
        !HHMM.matches(day.closesAt) -> "Enter a closing time like 22:30"
        day.opensAt == day.closesAt -> "Opening and closing can't be the same time"
        else -> null
    }

    fun isOvernight(day: DayHours): Boolean = day.open && errorFor(day) == null && day.closesAt < day.opensAt

    /** Errors keyed by day of week, plus [ALL_CLOSED]. */
    fun validate(week: List<DayHours>): Map<Int, String> = buildMap {
        week.forEach { day -> errorFor(day)?.let { put(day.dayOfWeek, it) } }
        if (week.none { it.open }) put(ALL_CLOSED, "Open on at least one day")
    }

    /** The whole week, Sunday first — `PUT …/operating-hours` replaces every day. */
    fun toRequest(week: List<DayHours>): OperatingHoursRequest = OperatingHoursRequest(
        windows = week.sortedBy { it.dayOfWeek }.map {
            OperatingWindowRequest(
                dayOfWeek = it.dayOfWeek,
                opensAt = if (it.open) it.opensAt else "",
                closesAt = if (it.open) it.closesAt else "",
                isClosed = !it.open,
            )
        },
    )

    /** A server field such as `windows[3].opens_at` → the day that window carried in [toRequest]. */
    fun dayForServerField(field: String?, week: List<DayHours>): Int? {
        val index = field?.let { SERVER_WINDOW.find(it)?.groupValues?.get(1)?.toIntOrNull() } ?: return null
        return week.sortedBy { it.dayOfWeek }.getOrNull(index)?.dayOfWeek
    }
}
