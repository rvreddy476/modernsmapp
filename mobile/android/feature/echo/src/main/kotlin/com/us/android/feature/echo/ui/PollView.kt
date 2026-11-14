package com.us.android.feature.echo.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.echo.data.NotePoll
import com.us.android.feature.echo.data.pollCanVote
import com.us.android.feature.echo.data.pollFooter
import com.us.android.feature.echo.data.pollPercent
import com.us.android.feature.echo.data.pollShowsResults
import java.time.Instant

/**
 * A note's poll inside its card: before voting, one outlined pill per choice;
 * after voting (or once closed, or for the author) a bar per choice with its
 * share, the viewer's own choice ticked and the leader in bold. A count the
 * server withheld is never drawn.
 */
@Composable
@Suppress("LongMethod") // results bars and vote buttons, one card
fun PollView(
    poll: NotePoll,
    viewerIsAuthor: Boolean,
    now: Instant,
    onVote: ((Int) -> Unit)?,
    modifier: Modifier = Modifier,
) {
    val results = pollShowsResults(poll, viewerIsAuthor, now)
    val canVote = onVote != null && pollCanVote(poll, viewerIsAuthor, now)
    Column(
        modifier = modifier.fillMaxWidth().testTag(if (results) "echo_poll_results" else "echo_poll_choices"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        val leader = poll.options.maxByOrNull { it.votes ?: -1 }?.takeIf { it.votes != null }?.index
        poll.options.forEach { option ->
            if (results) {
                val percent = pollPercent(option, poll.totalVotes)
                val shape = RoundedCornerShape(UsTheme.radii.medium)
                Box(modifier = Modifier.fillMaxWidth().height(ROW_HEIGHT).testTag("echo_poll_option:${option.index}")) {
                    Box(
                        modifier = Modifier
                            .fillMaxWidth((percent ?: 0) / FULL)
                            .fillMaxHeight()
                            .clip(shape)
                            .background(
                                if (option.index == leader) {
                                    UsTheme.extended.echo.tint
                                } else {
                                    UsTheme.extended.fillSubtle
                                },
                            ),
                    )
                    Row(
                        modifier = Modifier.fillMaxWidth().padding(
                            horizontal = UsTheme.spacing.l
                        ).align(Alignment.CenterStart),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(
                            text = option.label,
                            style = MaterialTheme.typography.bodyMedium,
                            fontWeight = if (option.index == leader) FontWeight.Bold else FontWeight.Normal,
                            color = UsTheme.extended.textPrimary,
                            modifier = Modifier.weight(1f),
                        )
                        if (poll.viewerVote == option.index) {
                            Icon(
                                UsIcons.Check,
                                contentDescription = "Your vote",
                                tint = UsTheme.extended.textPrimary,
                                modifier = Modifier.padding(end = UsTheme.spacing.s).size(CHECK),
                            )
                        }
                        if (percent != null) {
                            Text(
                                text = "$percent%",
                                style = MaterialTheme.typography.bodyMedium,
                                fontWeight = if (option.index == leader) FontWeight.Bold else FontWeight.Normal,
                                color = UsTheme.extended.textPrimary,
                                modifier = Modifier.testTag("echo_poll_percent:${option.index}"),
                            )
                        }
                    }
                }
            } else {
                val shape = RoundedCornerShape(UsTheme.radii.full)
                Text(
                    text = option.label,
                    style = MaterialTheme.typography.bodyMedium,
                    fontWeight = FontWeight.Bold,
                    color = if (canVote) UsTheme.extended.echo.text else UsTheme.extended.textTertiary,
                    modifier = Modifier
                        .fillMaxWidth()
                        .clip(shape)
                        .border(
                            1.dp,
                            if (canVote) UsTheme.extended.echo.border else UsTheme.extended.borderMedium,
                            shape,
                        )
                        .clickable(enabled = canVote) { onVote?.invoke(option.index) }
                        .padding(vertical = UsTheme.spacing.m, horizontal = UsTheme.spacing.l)
                        .testTag("echo_poll_vote:${option.index}"),
                    textAlign = androidx.compose.ui.text.style.TextAlign.Center,
                )
            }
        }
        Text(
            text = pollFooter(poll, now),
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textTertiary,
        )
    }
}

private const val FULL = 100f
private val ROW_HEIGHT = 34.dp
private val CHECK = 16.dp
