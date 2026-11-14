package com.us.android.feature.echo.ui

import android.content.Context
import android.content.Intent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.WebLinks
import com.us.android.feature.echo.data.Note

/**
 * Why this viewer cannot post or reply, in plain words.
 *
 * Google Play's payments policy for selling Premium in-app is still being
 * checked (founder decision 4), so by default there is no purchase button and
 * no link out: OK only. "Get Premium" is drawn above OK only when the reason
 * is Premium AND [premiumEntryShown] — `:app`'s purchase switch is on and the
 * server's premium plans answered 200 (13 Nov 2026).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun EligibilitySheet(
    blocked: ComposeGate.Blocked,
    onDismiss: () -> Unit,
    premiumEntryShown: () -> Boolean = { false },
    onGetPremium: () -> Unit = {},
) {
    ModalBottomSheet(onDismissRequest = onDismiss, containerColor = UsTheme.extended.bgCardSolid) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
                .testTag("echo_eligibility_sheet"),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            Text(
                text = blocked.title,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = blocked.detail,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary
            )
            if (showGetPremium(blocked, premiumEntryShown())) {
                UsButton(
                    text = GET_PREMIUM,
                    onClick = {
                        onDismiss()
                        onGetPremium()
                    },
                    modifier = Modifier.fillMaxWidth().testTag("echo_eligibility_get_premium"),
                )
                UsSecondaryButton(
                    text = "OK",
                    onClick = onDismiss,
                    modifier = Modifier.fillMaxWidth().testTag("echo_eligibility_ok"),
                )
            } else {
                UsButton(
                    text = "OK",
                    onClick = onDismiss,
                    modifier = Modifier.fillMaxWidth().testTag("echo_eligibility_ok")
                )
            }
        }
    }
}

/** The system chooser with the note's web address, `https://cleestudio.com/echo/note/{id}`. */
@Composable
fun rememberNoteSharer(): (Note) -> Unit {
    val context = LocalContext.current
    return remember(context) { { note -> context.shareNote(note) } }
}

/** "Alice Rao on Echo: Rain all week… https://cleestudio.com/echo/note/{id}?ref=…". */
fun noteShareText(note: Note): String {
    val link = WebLinks.withShareRef(WebLinks.echoNotePermalink(note.id))
    val who = note.author?.name?.takeIf { it.isNotBlank() }
    val excerpt = note.text.trim().take(
        SHARE_EXCERPT
    ).let { if (note.text.trim().length > SHARE_EXCERPT) "$it…" else it }
    val head = listOfNotNull(who?.let { "$it on Echo" }, excerpt.takeIf { it.isNotBlank() }).joinToString(": ")
    return if (head.isBlank()) link else "$head\n$link"
}

/** Opens the system chooser; true when it opened (only then is the share counted). */
internal fun Context.shareNote(note: Note): Boolean {
    val send = Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_TEXT, noteShareText(note))
    }
    return runCatching {
        startActivity(Intent.createChooser(send, null).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }.isSuccess
}

private const val SHARE_EXCERPT = 120

/** The sheet's purchase button, the web's words. */
const val GET_PREMIUM = "Get Premium"

/**
 * Whether the sheet offers "Get Premium": only for the Premium reason, and only
 * while `:app` draws a way in ([entryShown]). Pure, so it is a table test.
 */
fun showGetPremium(blocked: ComposeGate.Blocked, entryShown: Boolean): Boolean = entryShown && blocked.premiumRequired

/** A [UsMessageHost] for hosts drawn outside a Box (sheets, gates): it lays its own full-size Box. */
@Composable
internal fun EchoMessageHost(message: UsMessage?, onDismiss: () -> Unit) {
    Box(modifier = Modifier.fillMaxSize()) {
        UsMessageHost(message = message, onDismiss = onDismiss)
    }
}
