package com.us.android.feature.feed.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.publish.ReelPublishState

/**
 * The slim strip that says a reel is posting — pinned under the feed's tabs
 * row and over the top of the Reels pager.
 *
 * TikTok and Instagram return you to the feed the moment you tap Post and
 * show the upload as a thin bar; this is that. One line of Figtree, an ember
 * rail underneath that fills with the upload and slides while the server
 * works, then "Your reel is live" with View, which goes away by itself.
 * A failure stays until Retry or Discard: it is the one state that needs a
 * decision.
 */
@Composable
internal fun ReelPublishBanner(
    onOpenPost: (postId: String) -> Unit,
    modifier: Modifier = Modifier,
    viewModel: ReelPublishBannerViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    ReelPublishBannerContent(
        state = state,
        onView = { postId ->
            viewModel.dismiss()
            onOpenPost(postId)
        },
        onRetry = viewModel::retry,
        onDiscard = viewModel::discard,
        modifier = modifier,
    )
}

@Composable
internal fun ReelPublishBannerContent(
    state: ReelPublishState,
    onView: (postId: String) -> Unit,
    onRetry: () -> Unit,
    onDiscard: () -> Unit,
    modifier: Modifier = Modifier,
) {
    // The last real state is kept so the exit animation shrinks the banner
    // that was showing, not an empty one.
    var shown by remember { mutableStateOf(state) }
    if (state != ReelPublishState.Idle) shown = state

    AnimatedVisibility(
        visible = state != ReelPublishState.Idle,
        enter = expandVertically() + fadeIn(),
        exit = shrinkVertically() + fadeOut(),
        modifier = modifier,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .background(UsTheme.extended.bgRaised)
                .semantics { liveRegion = LiveRegionMode.Polite }
                .testTag("reel-publish-banner"),
        ) {
            BannerLine(state = shown, onView = onView, onRetry = onRetry, onDiscard = onDiscard)
            ProgressRail(state = shown)
        }
    }
}

@Composable
private fun BannerLine(
    state: ReelPublishState,
    onView: (postId: String) -> Unit,
    onRetry: () -> Unit,
    onDiscard: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = LINE_PADDING_V),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (state is ReelPublishState.Published) {
            Icon(
                imageVector = UsIcons.Check,
                contentDescription = null,
                tint = UsTheme.extended.statusSuccess,
                modifier = Modifier.size(GLYPH),
            )
            Spacer(Modifier.width(UsTheme.spacing.s))
        }
        Text(
            text = state.title(),
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            modifier = Modifier.testTag("reel-publish-title"),
        )
        Spacer(Modifier.width(UsTheme.spacing.m))
        Text(
            text = state.detail(),
            style = MaterialTheme.typography.bodyMedium.copy(fontSize = DETAIL_SIZE),
            color = UsTheme.extended.textMuted,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier
                .weight(1f)
                .testTag("reel-publish-detail"),
        )
        when (state) {
            is ReelPublishState.Published -> BannerAction(
                text = "View",
                color = UsTheme.extended.accentSolid,
                onClick = { onView(state.postId) },
                testTag = "reel-publish-view",
            )
            is ReelPublishState.Failed -> {
                if (state.retryable) {
                    BannerAction(
                        text = "Retry",
                        color = UsTheme.extended.accentSolid,
                        onClick = onRetry,
                        testTag = "reel-publish-retry",
                    )
                }
                BannerAction(
                    text = "Discard",
                    color = UsTheme.extended.textMuted,
                    onClick = onDiscard,
                    testTag = "reel-publish-discard",
                )
            }
            else -> Unit
        }
    }
}

/**
 * The 2dp ember rail. Determinate for the upload; a sliding segment while
 * the server transcodes and the post goes out — the honest shape of "we're
 * waiting on someone else"; full for a live reel; nothing for a failure.
 */
@Composable
private fun ProgressRail(state: ReelPublishState) {
    val brush = UsTheme.extended.ctaGradient
    val track = UsTheme.extended.bgCard
    val fraction by animateFloatAsState(
        targetValue = when (state) {
            is ReelPublishState.Uploading -> state.fraction
            is ReelPublishState.Published -> 1f
            else -> 0f
        },
        label = "reelPublishFraction",
    )
    val indeterminate = state is ReelPublishState.Preparing ||
        state is ReelPublishState.Processing ||
        state is ReelPublishState.Posting
    val slide by rememberInfiniteTransition(label = "reelPublishSlide").animateFloat(
        initialValue = 0f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = SLIDE_MILLIS, easing = LinearEasing),
            repeatMode = RepeatMode.Restart,
        ),
        label = "reelPublishSlideOffset",
    )
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .height(RAIL_HEIGHT)
            .background(track)
            .drawBehind {
                if (indeterminate) {
                    val segment = size.width * SEGMENT_FRACTION
                    val x = (slide * (1f + SEGMENT_FRACTION) - SEGMENT_FRACTION) * size.width
                    drawRect(brush = brush, topLeft = Offset(x, 0f), size = Size(segment, size.height))
                } else if (fraction > 0f) {
                    drawRect(brush = brush, size = Size(size.width * fraction, size.height))
                }
            }
            .testTag("reel-publish-rail"),
    )
}

/** A text action with the form's press-dim instead of a ripple. */
@Composable
private fun BannerAction(
    text: String,
    color: Color,
    onClick: () -> Unit,
    testTag: String,
) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val alpha by animateFloatAsState(targetValue = if (pressed) PRESS_ALPHA else 1f, label = "bannerActionPress")
    Text(
        text = text,
        style = MaterialTheme.typography.labelLarge,
        fontWeight = FontWeight.SemiBold,
        color = color,
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .graphicsLayer { this.alpha = alpha }
            .padding(horizontal = ACTION_PADDING_H, vertical = ACTION_PADDING_V)
            .semantics { role = Role.Button }
            .testTag(testTag),
    )
}

private fun ReelPublishState.title(): String = when (this) {
    is ReelPublishState.Published -> "Your reel is live"
    is ReelPublishState.Failed -> "Couldn't post your reel"
    else -> "Posting your reel…"
}

private fun ReelPublishState.detail(): String = when (this) {
    ReelPublishState.Preparing -> "Preparing video…"
    is ReelPublishState.Uploading -> "Uploading ${(fraction * PERCENT).toInt()}%"
    ReelPublishState.Processing -> "Processing video…"
    ReelPublishState.Posting -> "Posting…"
    is ReelPublishState.Failed -> message
    is ReelPublishState.Published, ReelPublishState.Idle -> ""
}

@Preview(name = "Reel banner — uploading", showBackground = true, backgroundColor = 0xFF041122)
@Composable
private fun ReelPublishBannerUploadingPreview() {
    UsTheme {
        ReelPublishBannerContent(
            state = ReelPublishState.Uploading(PREVIEW_FRACTION),
            onView = {},
            onRetry = {},
            onDiscard = {},
        )
    }
}

@Preview(name = "Reel banner — failed", showBackground = true, backgroundColor = 0xFF041122)
@Composable
private fun ReelPublishBannerFailedPreview() {
    UsTheme {
        ReelPublishBannerContent(
            state = ReelPublishState.Failed("The upload didn't finish. Try again.", retryable = true),
            onView = {},
            onRetry = {},
            onDiscard = {},
        )
    }
}

private const val PERCENT = 100
private const val PREVIEW_FRACTION = 0.42f
private const val PRESS_ALPHA = 0.6f
private const val SEGMENT_FRACTION = 0.3f
private const val SLIDE_MILLIS = 1_400
private val RAIL_HEIGHT = 2.dp
private val GLYPH = 14.dp
private val LINE_PADDING_V = 9.dp
private val ACTION_PADDING_H = 10.dp
private val ACTION_PADDING_V = 4.dp
private val DETAIL_SIZE = 12.sp
