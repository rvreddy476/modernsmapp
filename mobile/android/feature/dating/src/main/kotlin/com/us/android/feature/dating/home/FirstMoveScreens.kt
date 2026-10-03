package com.us.android.feature.dating.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.State
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.Pill
import com.us.android.feature.dating.ui.Tone
import kotlinx.coroutines.delay
import java.time.Instant
import java.time.ZoneId

/** The clock the countdowns read, moved on every [tickMillis]. */
@Composable
internal fun rememberNow(tickMillis: Long = NOW_TICK_MILLIS): State<Instant> = produceState(Instant.now(), tickMillis) {
    while (true) {
        delay(tickMillis)
        value = Instant.now()
    }
}

/** A first-move match row's mark: who starts, and the time left. */
@Composable
internal fun FirstMoveRowTag(firstMove: FirstMoveUi, now: Instant) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        if (firstMove.youMoveFirst) {
            Pill(FirstMoveCopy.YOU_START_TAG, Tone.Accent)
        } else {
            Pill(FirstMoveCopy.WAITING_TAG, Tone.Warning)
        }
        FirstMoveCopy.timeLeft(firstMove.deadline, now)?.let {
            Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        }
    }
}

@Composable
private fun Countdown(deadline: Instant?, now: Instant) {
    val left = FirstMoveCopy.timeLeft(deadline, now) ?: return
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
        Icon(UsIcons.Clock, contentDescription = null, tint = UsTheme.extended.statusWarning, modifier = Modifier.size(16.dp))
        Text(left, style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.statusWarning)
    }
}

/** The viewer writes first: say so, show the clock, and the chat is the way in. */
@Composable
internal fun YouStartPanel(firstMove: FirstMoveUi, now: Instant, onOpenChat: () -> Unit) {
    DatingCard {
        Text(FirstMoveCopy.YOU_START_TITLE, style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
        Text(FirstMoveCopy.YOU_START_BODY, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
        Countdown(firstMove.deadline, now)
    }
    UsButton(text = "Write the first message", onClick = onOpenChat, modifier = Modifier.fillMaxWidth())
}

/** What the waiting person can do: answer an opening question, or give the other person more time. */
internal class WaitingActions(
    val onStartAnswer: (String) -> Unit,
    val onEditAnswer: (String) -> Unit,
    val onSendAnswer: () -> Unit,
    val onCancelAnswer: () -> Unit,
    val onExtend: () -> Unit,
)

/** The other person writes first: no "Open chat" here, only their questions and the free extend. */
@Composable
internal fun WaitingPanel(
    name: String?,
    firstMove: FirstMoveUi,
    actions: FirstMoveActionsUi,
    now: Instant,
    on: WaitingActions,
) {
    DatingCard {
        Text(FirstMoveCopy.WAITING_TITLE, style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
        Text(FirstMoveCopy.waitingBody(name), style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
        Countdown(firstMove.deadline, now)
    }
    if (firstMove.questions.isEmpty()) {
        InfoNote(FirstMoveCopy.WAITING_NO_QUESTIONS)
    }
    firstMove.questions.forEach { question ->
        DatingCard {
            Text("“${question.text}”", style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
            if (actions.answeringId == question.id) {
                AnswerComposer(actions, on)
            } else {
                TextButton(onClick = { on.onStartAnswer(question.id) }, enabled = !actions.sending) {
                    Text(FirstMoveCopy.ANSWER, color = UsTheme.extended.accentSolid)
                }
            }
        }
    }
    when {
        firstMove.canExtend -> UsSecondaryButton(
            text = FirstMoveCopy.EXTEND,
            onClick = on.onExtend,
            enabled = !actions.extending,
            modifier = Modifier.fillMaxWidth(),
        )
        actions.extendLimit != null -> InfoNote(FirstMoveCopy.extendSpent(actions.extendLimit, now, ZoneId.systemDefault()))
    }
}

@Composable
private fun AnswerComposer(actions: FirstMoveActionsUi, on: WaitingActions) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
        UsTextField(
            value = actions.answer,
            onValueChange = on.onEditAnswer,
            label = "Your answer",
            singleLine = false,
            enabled = !actions.sending,
            errorText = actions.answerError,
        )
        Text(
            "${actions.answer.length}/${FirstMoveCopy.MAX_ANSWER_LENGTH}",
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textDim,
            modifier = Modifier.align(Alignment.End),
        )
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            UsButton(text = "Send answer", onClick = on.onSendAnswer, loading = actions.sending, modifier = Modifier.weight(1f))
            TextButton(onClick = on.onCancelAnswer, enabled = !actions.sending) {
                Text("Cancel", color = UsTheme.extended.textMuted)
            }
        }
    }
}

private const val NOW_TICK_MILLIS = 30_000L
