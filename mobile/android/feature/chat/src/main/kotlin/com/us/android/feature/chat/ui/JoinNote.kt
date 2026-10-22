package com.us.android.feature.chat.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextAlign
import com.us.android.core.chat.data.JoinMethod
import com.us.android.core.chat.data.MyJoin
import com.us.android.core.designsystem.theme.UsTheme
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/*
 * The "you joined" note at the top of a group chat (founder, 2026-10-22).
 *
 * A group newcomer sees only the messages sent after they joined, as on
 * WhatsApp. Above the oldest message (or alone in an empty chat) one small
 * centred line says how they got in, and a second says why nothing earlier
 * is there:
 *
 *   invite link   You joined using this group's invite link · 22 Oct
 *   added         Asha added you · 22 Oct        (Someone, when unknown)
 *   invitation    You joined · 22 Oct
 */

/** The second line under the join note. */
const val JOIN_NOTE_HISTORY_TEXT = "Messages sent before you joined aren't shown."

/** Day + short month; the year joins only when it is not this year. */
const val JOIN_NOTE_DAY_SKELETON = "dMMM"
const val JOIN_NOTE_YEAR_SKELETON = "dMMMy"

/**
 * "22 Oct" for [joinedAt] in [zone], formatted with the locale's own pattern
 * for the skeleton ([dayPattern] this year, [yearPattern] otherwise); blank
 * for a date that does not parse.
 */
fun joinNoteDate(
    joinedAt: String,
    locale: Locale,
    zone: ZoneId,
    today: LocalDate,
    dayPattern: String = "d MMM",
    yearPattern: String = "d MMM yyyy",
): String {
    val at = runCatching { OffsetDateTime.parse(joinedAt).atZoneSameInstant(zone).toLocalDate() }.getOrNull()
        ?: return ""
    val pattern = if (at.year == today.year) dayPattern else yearPattern
    return runCatching { at.format(DateTimeFormatter.ofPattern(pattern, locale)) }.getOrDefault("")
}

/** The note's first line; [adderName] is the adder's display name (blank → "Someone"). */
fun joinNoteText(myJoin: MyJoin, adderName: String?, date: String): String {
    val lead = when (myJoin.method) {
        JoinMethod.InviteLink -> "You joined using this group's invite link"
        JoinMethod.Added -> "${adderName?.trim().takeUnless { it.isNullOrEmpty() } ?: "Someone"} added you"
        JoinMethod.Invitation -> "You joined"
    }
    return if (date.isBlank()) lead else "$lead · $date"
}

/** The note itself, in the call-log line's muted label style. */
@Composable
internal fun JoinNoteRow(myJoin: MyJoin, adderName: String?, modifier: Modifier = Modifier) {
    val locale = Locale.getDefault()
    val date = remember(myJoin.joinedAt, locale) {
        joinNoteDate(
            joinedAt = myJoin.joinedAt,
            locale = locale,
            zone = ZoneId.systemDefault(),
            today = LocalDate.now(),
            dayPattern = android.text.format.DateFormat.getBestDateTimePattern(locale, JOIN_NOTE_DAY_SKELETON),
            yearPattern = android.text.format.DateFormat.getBestDateTimePattern(locale, JOIN_NOTE_YEAR_SKELETON),
        )
    }
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .testTag("join-note"),
    ) {
        Text(
            text = joinNoteText(myJoin, adderName, date),
            style = MaterialTheme.typography.labelMedium,
            color = UsTheme.extended.textMuted,
            textAlign = TextAlign.Center,
        )
        Text(
            text = JOIN_NOTE_HISTORY_TEXT,
            style = MaterialTheme.typography.labelMedium,
            color = UsTheme.extended.textMuted,
            textAlign = TextAlign.Center,
        )
    }
}
