package com.us.android.core.designsystem.component

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.AnimationSpec
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.snap
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.Orientation
import androidx.compose.foundation.gestures.draggable
import androidx.compose.foundation.gestures.rememberDraggableState
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.compose.ui.window.DialogWindowProvider
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.launch

/**
 * Momentum's end-anchored drawer (the Me tab's ☰, 2026-10-17): a panel that
 * slides in from the right over a scrim, [WIDTH_FRACTION] of the screen wide,
 * full height and over the bottom bar.
 *
 * It closes on a scrim tap, Back, or a swipe toward the edge it came from;
 * a swipe that stops short springs back. Every close slides the panel away
 * FIRST: [content] receives `close(then)`, which runs `then` only once the
 * panel is gone, so what a row opens lands on a clear screen (the rule the
 * sheets follow). With the system's "remove animations" setting on, it
 * appears and leaves without moving.
 *
 * Drawn in its own window, edge to edge, so it covers the shell's bar; the
 * panel pads itself by the system bars and the cutout on the sides it
 * touches.
 */
@Composable
fun UsEndDrawer(
    onDismiss: () -> Unit,
    modifier: Modifier = Modifier,
    content: @Composable ColumnScope.(close: (then: () -> Unit) -> Unit) -> Unit,
) {
    val reducedMotion = UsTheme.reducedMotion
    val spec: AnimationSpec<Float> = if (reducedMotion) snap() else tween(SLIDE_MILLIS, easing = FastOutSlowInEasing)
    // 0 is open, 1 is fully off the end edge.
    val hidden = remember { Animatable(1f) }
    val scope = rememberCoroutineScope()
    val close: (then: () -> Unit) -> Unit = { then ->
        scope.launch {
            hidden.animateTo(1f, spec)
            onDismiss()
            then()
        }
    }

    LaunchedEffect(Unit) { hidden.animateTo(0f, spec) }

    Dialog(
        onDismissRequest = { close {} },
        properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false),
    ) {
        // The window's own dim would sit under ours and not follow the swipe.
        val window = (LocalView.current.parent as? DialogWindowProvider)?.window
        SideEffect { window?.setDimAmount(0f) }

        BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
            val panelWidth = maxWidth * WIDTH_FRACTION
            val density = LocalDensity.current
            val panelPx = with(density) { panelWidth.toPx() }.coerceAtLeast(1f)
            val flingPx = with(density) { FLING_SPEED.toPx() }
            val drag = rememberDraggableState { delta ->
                scope.launch { hidden.snapTo((hidden.value + delta / panelPx).coerceIn(0f, 1f)) }
            }

            // The scrim: dims with the panel, closes on a tap.
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .graphicsLayer { alpha = 1f - hidden.value }
                    .background(UsTheme.extended.scrim)
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                        role = Role.Button,
                        onClickLabel = CLOSE_LABEL,
                    ) { close {} }
                    .semantics { contentDescription = CLOSE_LABEL }
                    .testTag("end_drawer_scrim"),
            )

            Column(
                modifier = modifier
                    .align(Alignment.CenterEnd)
                    .width(panelWidth)
                    .fillMaxHeight()
                    .graphicsLayer { translationX = hidden.value * panelPx }
                    .clip(RoundedCornerShape(topStart = UsTheme.radii.card, bottomStart = UsTheme.radii.card))
                    .background(UsTheme.extended.bgSheet)
                    .draggable(
                        state = drag,
                        orientation = Orientation.Horizontal,
                        onDragStopped = { velocity ->
                            if (endDrawerCloses(hidden.value, velocity, flingPx)) {
                                close {}
                            } else {
                                hidden.animateTo(0f, spec)
                            }
                        },
                    )
                    .windowInsetsPadding(
                        WindowInsets.safeDrawing.only(WindowInsetsSides.Vertical + WindowInsetsSides.End),
                    )
                    .verticalScroll(rememberScrollState())
                    .testTag("end_drawer_panel"),
                content = { content(close) },
            )
        }
    }
}

/**
 * Whether a released swipe closes the drawer: it was dragged past
 * [CLOSE_FRACTION] of its width toward the end edge, or flung that way at
 * [flingPxPerSecond] or faster ([velocity] is positive toward the edge).
 * Short of both, the panel springs back. Pure, so it is a table test.
 */
fun endDrawerCloses(hiddenFraction: Float, velocity: Float, flingPxPerSecond: Float): Boolean =
    hiddenFraction > CLOSE_FRACTION || velocity >= flingPxPerSecond

/** The share of the screen the panel covers. */
private const val WIDTH_FRACTION = 0.82f

/** Released past this share of its width toward the edge, the panel closes. */
private const val CLOSE_FRACTION = 0.35f

private const val SLIDE_MILLIS = 260

/** A release at least this fast (per second) toward the edge closes the panel however far it moved. */
private val FLING_SPEED = 600.dp

private const val CLOSE_LABEL = "Close menu"
