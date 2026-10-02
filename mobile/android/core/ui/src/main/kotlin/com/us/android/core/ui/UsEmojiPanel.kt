package com.us.android.core.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The emoji panel: a curated grid, one tap puts an emoji into a draft.
 *
 * It was the chat thread's own (a private composable in `:feature:chat`);
 * 2026-10-02 it moved here so the live chat's composer can offer the same
 * panel behind the same smiley, and a third copy is never written. Kept
 * in-app rather than relying on the keyboard's own emoji page — the point
 * of the smiley button is that emoji are ONE tap away, not three, and that
 * they are there whatever keyboard the phone has.
 *
 * [onPick] receives the emoji as the string to append. Stateless.
 */
@Composable
fun UsEmojiPanel(onPick: (String) -> Unit, modifier: Modifier = Modifier) {
    LazyVerticalGrid(
        columns = GridCells.Fixed(EMOJI_COLUMNS),
        modifier = modifier
            .fillMaxWidth()
            .height(EMOJI_PANEL_HEIGHT)
            .background(UsTheme.extended.bgCardSolid),
        contentPadding = PaddingValues(UsTheme.spacing.m),
    ) {
        items(US_EMOJI_CHOICES) { emoji ->
            Text(
                text = emoji,
                style = MaterialTheme.typography.headlineSmall,
                textAlign = TextAlign.Center,
                modifier = Modifier
                    .clip(RoundedCornerShape(UsTheme.radii.small))
                    .clickable { onPick(emoji) }
                    .padding(UsTheme.spacing.s)
                    .semantics { contentDescription = "Insert $emoji" },
            )
        }
    }
}

/**
 * The panel's emoji — a curated set across the categories people actually
 * send, not a full unicode browser. The keyboard remains the long tail;
 * this is the fast path.
 */
val US_EMOJI_CHOICES: List<String> = listOf(
    "😀", "😂", "🤣", "😊", "😍", "😘", "😎", "🤩",
    "😅", "😉", "🙃", "😇", "🥰", "😜", "🤔", "🙄",
    "😴", "🥺", "😢", "😭", "😡", "🤯", "😱", "🥳",
    "👍", "👎", "👏", "🙌", "🙏", "🤝", "💪", "✌️",
    "👀", "🔥", "✨", "🎉", "🚀", "❤️", "💔", "💯",
    "😋", "🍕", "☕", "🍻", "🎂", "🌟", "🌈", "☀️",
)

private const val EMOJI_COLUMNS = 8
private val EMOJI_PANEL_HEIGHT = 220.dp

@Preview
@Composable
private fun UsEmojiPanelPreview() {
    UsTheme { UsEmojiPanel(onPick = {}) }
}
