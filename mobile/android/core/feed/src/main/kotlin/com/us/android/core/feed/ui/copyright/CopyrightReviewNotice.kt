package com.us.android.core.feed.ui.copyright

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.copyright.over
import com.us.android.core.feed.ui.edit.OwnerSheet
import com.us.android.core.feed.ui.edit.SheetNote
import com.us.android.core.model.CopyrightCopy
import com.us.android.core.model.CopyrightNotice
import com.us.android.core.model.CopyrightReview
import com.us.android.core.model.CopyrightReviewState

/**
 * The author's copyright notice with its own "Request review" (copyright
 * title review, 2026-10-24) — for a surface that shows ONE video: the Edit
 * sheet and the watch screen. [review] is what the surface read; a request
 * sent since (here or anywhere) is layered over it. Draws nothing when
 * there is no review.
 */
@Composable
fun CopyrightReviewPanel(
    postId: String,
    review: CopyrightReview?,
    modifier: Modifier = Modifier,
    viewModel: CopyrightRequestViewModel = hiltViewModel(key = "copyright:$postId"),
) {
    val reviews by viewModel.reviews.collectAsStateWithLifecycle()
    val shown = reviews.over(postId, review) ?: return
    var requesting by rememberSaveable(postId) { mutableStateOf(false) }
    val notice = remember(shown) { CopyrightCopy.notice(shown) }
    CopyrightNoticeCard(
        notice = notice,
        onRequest = { requesting = true },
        modifier = modifier,
        testTag = "copyright_notice:$postId",
    )
    if (requesting) {
        CopyrightRequestSheet(postId = postId, onDismiss = { requesting = false }, viewModel = viewModel)
    }
}

/**
 * The notice itself, stateless: the sentence in the state's colour, the
 * "Review requested <date>" line once asked, and "Request review" while it
 * may still be asked. A list row draws this and hosts one sheet itself.
 */
@Composable
fun CopyrightNoticeCard(
    notice: CopyrightNotice,
    onRequest: () -> Unit,
    modifier: Modifier = Modifier,
    testTag: String = "copyright_notice",
) {
    Row(
        modifier = modifier.fillMaxWidth().testTag(testTag),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalAlignment = Alignment.Top,
    ) {
        Icon(
            imageVector = notice.state.glyph(),
            contentDescription = null,
            tint = notice.state.tint(),
            modifier = Modifier.size(GLYPH),
        )
        Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Text(
                text = notice.message,
                style = MaterialTheme.typography.bodySmall,
                color = notice.state.tint(),
                modifier = Modifier.testTag("$testTag:message"),
            )
            notice.requested?.let {
                Text(
                    text = it,
                    style = MaterialTheme.typography.labelSmall,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.testTag("$testTag:requested"),
                )
            }
            if (notice.offersRequest) {
                UsPillButton(
                    text = CopyrightCopy.REQUEST_LABEL,
                    onClick = onRequest,
                    filled = false,
                    modifier = Modifier.testTag("$testTag:request"),
                )
            }
        }
    }
}

/**
 * "Request review": the author's note (1–1000 characters) and Send. Leaves
 * once the server has it; the notice then reads "Review requested <date>".
 */
@Composable
fun CopyrightRequestSheet(
    postId: String,
    onDismiss: () -> Unit,
    viewModel: CopyrightRequestViewModel = hiltViewModel(key = "copyright:$postId"),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    // A fresh note each opening; only a request sent from THIS opening closes it.
    val sentBefore = remember { state.sent }
    LaunchedEffect(Unit) { viewModel.open() }
    LaunchedEffect(state.sent) { if (state.sent != null && state.sent !== sentBefore) onDismiss() }
    OwnerSheet(
        title = CopyrightCopy.REQUEST_LABEL,
        subtitle = "Tell our team why this video is yours to post.",
        icon = UsIcons.ShieldCheck,
        testTag = "copyright_request_sheet",
        onDismiss = onDismiss,
    ) {
        UsTextField(
            value = state.note,
            onValueChange = viewModel::changeNote,
            label = "Your note",
            placeholder = CopyrightCopy.NOTE_PLACEHOLDER,
            singleLine = false,
            enabled = !state.sending,
            modifier = Modifier.fillMaxWidth().testTag("copyright_request_note"),
        )
        Text(
            text = "${state.remaining} characters left",
            style = MaterialTheme.typography.labelSmall,
            color = if (state.remaining < 0) MaterialTheme.colorScheme.error else UsTheme.extended.textMuted,
        )
        state.problem?.let { SheetNote(text = it, isError = true, testTag = "copyright_request_problem") }
        UsButton(
            text = "Send request",
            onClick = { viewModel.send(postId) },
            enabled = state.canSend,
            loading = state.sending,
            modifier = Modifier.fillMaxWidth().testTag("copyright_request_send"),
        )
    }
}

@Composable
private fun CopyrightReviewState.tint(): Color = when (this) {
    CopyrightReviewState.PENDING -> UsTheme.extended.statusWarning
    CopyrightReviewState.CLEARED -> UsTheme.extended.statusSuccess
    CopyrightReviewState.BLOCKED -> UsTheme.extended.statusDanger
}

private fun CopyrightReviewState.glyph(): ImageVector = when (this) {
    CopyrightReviewState.CLEARED -> UsIcons.ShieldCheck
    CopyrightReviewState.PENDING, CopyrightReviewState.BLOCKED -> UsIcons.ShieldAlert
}

private val GLYPH = 18.dp
