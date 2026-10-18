package com.us.android.feature.chat.ui

import androidx.compose.animation.core.Animatable
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.Orientation
import androidx.compose.foundation.gestures.draggable
import androidx.compose.foundation.gestures.rememberDraggableState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch
import kotlin.math.abs
import kotlin.math.roundToInt

/** One button a swipe reveals under an inbox row. */
internal data class SwipeAction(
    val icon: ImageVector,
    val label: String,
    val tag: String,
    val fill: Color,
    val ink: Color,
    val onClick: () -> Unit,
)

/**
 * Where a released swipe comes to rest (fix pass 2): open on the side it
 * was pulled past half of that side's buttons, or flung hard toward; shut
 * otherwise. [offset] is positive when pulled right (revealing [startWidth]
 * of buttons on the left) and negative when pulled left. Pure, so the rule
 * is a unit test.
 */
fun swipeRestingOffset(offset: Float, velocity: Float, startWidth: Float, endWidth: Float): Float {
    val fling = abs(velocity) > SWIPE_FLING_VELOCITY
    return when {
        offset > 0f && startWidth > 0f && (offset > startWidth / 2 || (fling && velocity > 0f)) -> startWidth
        offset < 0f && endWidth > 0f && (-offset > endWidth / 2 || (fling && velocity < 0f)) -> -endWidth
        else -> 0f
    }
}

/**
 * An inbox row that swipes (fix pass 2, alongside long-press): pulled right
 * it shows [start] (Pin); pulled left it shows [end] (Mark read or unread,
 * Delete). A tap on a revealed button runs it and shuts the row. Every
 * action is also offered to accessibility services as a custom action, so
 * nobody needs the gesture.
 */
@Composable
internal fun SwipeableInboxRow(
    start: List<SwipeAction>,
    end: List<SwipeAction>,
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    val density = LocalDensity.current
    val startWidth = with(density) { (SWIPE_ACTION_WIDTH * start.size).toPx() }
    val endWidth = with(density) { (SWIPE_ACTION_WIDTH * end.size).toPx() }
    val offset = remember { Animatable(0f) }
    val scope = rememberCoroutineScope()
    val close: () -> Unit = { scope.launch { offset.animateTo(0f) } }
    Box(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = SWIPE_ROW_MARGIN)
            .clip(RoundedCornerShape(SWIPE_ROW_CORNER))
            .semantics {
                customActions = (start + end).map { action ->
                    CustomAccessibilityAction(action.label) {
                        action.onClick()
                        true
                    }
                }
            },
    ) {
        Row(modifier = Modifier.matchParentSize(), horizontalArrangement = Arrangement.Start) {
            if (offset.value > 0f) start.forEach { SwipeButton(it, close) }
        }
        Row(modifier = Modifier.matchParentSize(), horizontalArrangement = Arrangement.End) {
            if (offset.value < 0f) end.forEach { SwipeButton(it, close) }
        }
        Box(
            modifier = Modifier
                .offset { IntOffset(offset.value.roundToInt(), 0) }
                .background(com.us.android.core.designsystem.theme.UsTheme.extended.bgCanvas)
                .draggable(
                    orientation = Orientation.Horizontal,
                    state = rememberDraggableState { delta ->
                        scope.launch { offset.snapTo((offset.value + delta).coerceIn(-endWidth, startWidth)) }
                    },
                    onDragStopped = { velocity ->
                        offset.animateTo(swipeRestingOffset(offset.value, velocity, startWidth, endWidth))
                    },
                ),
        ) {
            content()
        }
    }
}

@Composable
private fun SwipeButton(action: SwipeAction, close: () -> Unit) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
        modifier = Modifier
            .width(SWIPE_ACTION_WIDTH)
            .fillMaxHeight()
            .background(action.fill)
            .clickable {
                close()
                action.onClick()
            }
            .testTag(action.tag),
    ) {
        Icon(imageVector = action.icon, contentDescription = null, tint = action.ink, modifier = Modifier.size(SWIPE_GLYPH))
        Text(
            text = action.label,
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.SemiBold,
            color = action.ink,
            maxLines = 1,
        )
    }
}

/** Pixels per second past which a short pull still opens (or shuts) the row. */
private const val SWIPE_FLING_VELOCITY = 1_200f
private val SWIPE_ACTION_WIDTH = 76.dp
private val SWIPE_GLYPH = 20.dp

// The row's own margin and corner (InboxRow.kt), so the revealed buttons sit inside the row's shape.
private val SWIPE_ROW_MARGIN = 12.dp
private val SWIPE_ROW_CORNER = 16.dp
