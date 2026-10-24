package com.us.android.feature.tube.ui.watch

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.VideoChapter
import com.us.android.core.model.formatChapterClock

/**
 * The chapter strip under the title (web parity, 2026-10-24: the web's
 * ChapterStrip): one pill per chapter with its start time, sideways, the
 * playing one inverted and kept in view. A tap seeks the player there.
 */
@Composable
internal fun ChapterStrip(
    chapters: List<VideoChapter>,
    currentIndex: Int,
    onSeek: (positionMs: Long) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (chapters.isEmpty()) return
    val list = rememberLazyListState()
    LaunchedEffect(currentIndex) {
        if (currentIndex >= 0) list.animateScrollToItem(currentIndex)
    }
    LazyRow(
        state = list,
        modifier = modifier
            .fillMaxWidth()
            .testTag("watch_chapters"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s),
    ) {
        itemsIndexed(chapters, key = { _, chapter -> chapter.startMs }) { index, chapter ->
            ChapterPill(chapter = chapter, current = index == currentIndex, onClick = { onSeek(chapter.startMs) })
        }
    }
}

@Composable
private fun ChapterPill(chapter: VideoChapter, current: Boolean, onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    val clock = formatChapterClock(chapter.startMs)
    val ink = if (current) UsTheme.extended.bgCardSolid else UsTheme.extended.textPrimary
    Row(
        modifier = Modifier
            .height(PILL_HEIGHT)
            .widthIn(max = PILL_MAX_WIDTH)
            .clip(shape)
            .background(if (current) UsTheme.extended.textPrimary else UsTheme.extended.glassBg, shape)
            .border(HAIRLINE, if (current) UsTheme.extended.textPrimary else UsTheme.extended.glassBorder, shape)
            .selectable(selected = current, role = Role.Button, onClick = onClick)
            .semantics { contentDescription = "Chapter at $clock: ${chapter.title}" }
            .padding(horizontal = UsTheme.spacing.m)
            .testTag("watch_chapter:${chapter.startMs}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Text(text = clock, style = MaterialTheme.typography.labelMedium, fontWeight = FontWeight.SemiBold, color = ink)
        Text(
            text = chapter.title,
            style = MaterialTheme.typography.labelMedium,
            color = ink,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** The playing chapter's name over the player's bottom row, as YouTube shows it. */
@Composable
internal fun ChapterLabel(chapter: VideoChapter?, modifier: Modifier = Modifier) {
    if (chapter == null) return
    Text(
        text = chapter.title,
        style = MaterialTheme.typography.labelMedium,
        fontWeight = FontWeight.SemiBold,
        color = UsTheme.extended.onMedia,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
        modifier = modifier
            .padding(horizontal = UsTheme.spacing.l)
            .testTag("watch_chapter_now"),
    )
}

private val PILL_HEIGHT = 32.dp
private val PILL_MAX_WIDTH = 240.dp
private val HAIRLINE = 1.dp
