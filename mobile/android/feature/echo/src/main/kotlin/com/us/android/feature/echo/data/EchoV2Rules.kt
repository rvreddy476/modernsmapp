package com.us.android.feature.echo.data

import java.time.Duration
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/*
 * Echo v2's limits (echo-v2-contract.md): X's own numbers, so nothing here is
 * invented. The client checks them first so the composer says why Post is
 * off; the server stays the authority and refuses with its own codes.
 */
object EchoPollRules {
    const val MIN_OPTIONS = 2
    const val MAX_OPTIONS = 4
    const val MAX_OPTION_CHARS = 25
    const val MIN_MINUTES = 5
    const val MAX_MINUTES = 7 * 24 * 60

    /** X's default poll length: one day. */
    const val DEFAULT_MINUTES = 24 * 60

    /** The lengths the composer offers, X's picker reduced to its common stops. */
    val LENGTHS: List<Int> = listOf(FIVE_MINUTES, HALF_HOUR, ONE_HOUR, SIX_HOURS, ONE_DAY, THREE_DAYS, MAX_MINUTES)

    private const val FIVE_MINUTES = 5
    private const val HALF_HOUR = 30
    private const val ONE_HOUR = 60
    private const val SIX_HOURS = 6 * ONE_HOUR
    private const val ONE_DAY = 24 * ONE_HOUR
    private const val THREE_DAYS = 3 * ONE_DAY
}

object EchoScheduleRules {
    /** At least 5 minutes ahead. */
    val MIN_AHEAD: Duration = Duration.ofMinutes(MIN_AHEAD_MINUTES)

    /** At most 18 months ahead (X), counted as 18 x 30 days so the edge never depends on the calendar. */
    val MAX_AHEAD: Duration = Duration.ofDays(MAX_AHEAD_DAYS)

    private const val MIN_AHEAD_MINUTES = 5L
    private const val MAX_AHEAD_DAYS = 18L * 30
}

/** Why this poll draft cannot be sent, or null when it can. */
fun pollProblem(options: List<String>, durationMinutes: Int): String? {
    val filled = options.map { it.trim() }
    return when {
        filled.size < EchoPollRules.MIN_OPTIONS || filled.size > EchoPollRules.MAX_OPTIONS ->
            "A poll has 2 to 4 choices."
        filled.any { it.isEmpty() } -> "Fill in every choice or remove it."
        filled.any { it.codePointCount(0, it.length) > EchoPollRules.MAX_OPTION_CHARS } ->
            "Choices can be up to ${EchoPollRules.MAX_OPTION_CHARS} characters."
        filled.map { it.lowercase() }.toSet().size != filled.size -> "Each choice must be different."
        durationMinutes < EchoPollRules.MIN_MINUTES || durationMinutes > EchoPollRules.MAX_MINUTES ->
            "A poll runs from 5 minutes to 7 days."
        else -> null
    }
}

/** Why [at] is not a time a note can be scheduled for, at [now]; null when it is. */
fun scheduleProblem(at: Instant, now: Instant): String? = when {
    at.isBefore(now.plus(EchoScheduleRules.MIN_AHEAD)) -> "Pick a time at least 5 minutes from now."
    at.isAfter(now.plus(EchoScheduleRules.MAX_AHEAD)) -> "You can schedule up to 18 months ahead."
    else -> null
}

/**
 * Whether a poll shows its results (bars and percentages) to this viewer
 * rather than the choices to vote on: once they voted, once it closed, or to
 * its author (X). Even then a count the server withheld (null) is never drawn
 * as a number.
 */
fun pollShowsResults(poll: NotePoll, viewerIsAuthor: Boolean, now: Instant): Boolean =
    poll.viewerVote != null || pollClosed(poll, now) || viewerIsAuthor

/** Closed by the server's flag, or because its end has passed on this clock. */
fun pollClosed(poll: NotePoll, now: Instant): Boolean {
    if (poll.closed) return true
    val end = parseInstant(poll.endsAt) ?: return false
    return !now.isBefore(end)
}

/** Whether the viewer may vote now: not their own poll, not voted, still open. */
fun pollCanVote(poll: NotePoll, viewerIsAuthor: Boolean, now: Instant): Boolean =
    !viewerIsAuthor && poll.viewerVote == null && !pollClosed(poll, now)

/** One option's share in whole percent, or null when its count is withheld. */
fun pollPercent(option: PollOption, total: Long): Int? {
    val votes = option.votes ?: return null
    if (total <= 0) return 0
    return ((votes * PERCENT + total / 2) / total).toInt().coerceIn(0, PERCENT.toInt())
}

/** "12 votes · 2 days left" / "Final results". */
fun pollFooter(poll: NotePoll, now: Instant): String {
    val votes = if (poll.totalVotes == 1L) "1 vote" else "${poll.totalVotes} votes"
    if (pollClosed(poll, now)) return "$votes · Final results"
    val end = parseInstant(poll.endsAt) ?: return votes
    val left = Duration.between(now, end)
    val text = when {
        left.toDays() >= 1 -> plural(left.toDays(), "day")
        left.toHours() >= 1 -> plural(left.toHours(), "hour")
        else -> plural(left.toMinutes().coerceAtLeast(1), "minute")
    }
    return "$votes · $text left"
}

/** "1 day", "6 hours", "5 minutes": a poll length as the composer offers it. */
fun pollLengthLabel(minutes: Int): String = when {
    minutes % MINUTES_PER_DAY == 0 -> plural((minutes / MINUTES_PER_DAY).toLong(), "day")
    minutes % MINUTES_PER_HOUR == 0 -> plural((minutes / MINUTES_PER_HOUR).toLong(), "hour")
    else -> plural(minutes.toLong(), "minute")
}

/** X's post page time: "9:30 AM · 12 Nov 2026", in the phone's zone. */
fun fullDate(createdAt: String, zone: ZoneId = ZoneId.systemDefault(), locale: Locale = Locale.getDefault()): String {
    val at = parseInstant(createdAt) ?: return ""
    val time = DateTimeFormatter.ofPattern("h:mm a", locale).withZone(zone).format(at)
    val day = DateTimeFormatter.ofPattern("d MMM yyyy", locale).withZone(zone).format(at)
    return "$time · $day"
}

/** "Joined November 2026". */
fun joinedLabel(
    createdAt: String,
    zone: ZoneId = ZoneId.systemDefault(),
    locale: Locale = Locale.getDefault()
): String {
    val at = parseInstant(createdAt) ?: return ""
    return "Joined " + DateTimeFormatter.ofPattern("MMMM yyyy", locale).withZone(zone).format(at)
}

/** "Will send on Fri, 14 Nov 2026 at 9:30 AM". */
fun scheduleLabel(at: Instant, zone: ZoneId = ZoneId.systemDefault(), locale: Locale = Locale.getDefault()): String =
    "Will send on " + DateTimeFormatter.ofPattern("EEE, d MMM yyyy 'at' h:mm a", locale).withZone(zone).format(at)

private fun plural(n: Long, unit: String) = if (n == 1L) "1 $unit" else "$n ${unit}s"

private const val PERCENT = 100L
private const val MINUTES_PER_HOUR = 60
private const val MINUTES_PER_DAY = 24 * 60
