// MatchingDeclarationName: this file is the full-screen post stage — the rail,
// the caption block, the carousel, the text card — and the value types they read.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.ui

import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.sizeIn
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.PagerState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicText
import androidx.compose.foundation.text.TextAutoSize
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.blur
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/*
 * The full-screen post stage (post-screens parity, 2026-10-18): what Home's
 * vertical feed and the Reels tab both draw over a post that fills the
 * screen. ONE rail and ONE caption block for both, so a like on Home and a
 * like on Reels are the same control in the same place.
 *
 * Everything here is drawn OVER media, so every colour is an over-media
 * token (`onMedia`, `mediaPlate`, `stage`) that does not invert with the
 * theme. Product-neutral: the feature decides which controls a post offers
 * and what each one does; these only draw them.
 */

/** One control on the rail: a glyph, what it is called, and the count or name under it. */
@Immutable
data class StageRailControl(
    /** Stable, lower-case: the control's test tag suffix ("like", "comment"…). */
    val key: String,
    val icon: ImageVector,
    /** What a screen reader calls it ("Like", "Liked"). */
    val description: String,
    /** What is printed under the glyph: the count, or the control's name; null for none. */
    val label: String?,
    /** The glyph's colour; null is the plain over-media white. */
    val tint: Color? = null,
    val onClick: () -> Unit,
)

/**
 * The author at the top of the rail: their avatar, which opens their
 * profile, and — while the viewer can still make the edge — a small "+"
 * badge under it that makes it ([badge]).
 */
@Immutable
data class StageRailAuthor(
    val name: String,
    val seed: String,
    val imageUrl: String? = null,
    val onOpen: () -> Unit,
    /** The "+" under the avatar, or null when there is no edge to make. */
    val badge: StageRailBadge? = null,
    /** How far the video has played, 0..1, drawn as a ring round the avatar; null for none. */
    val progress: Float? = null,
)

/** The "+" badge: what it does, said for a screen reader ("Follow", "Subscribe"), and the tap. */
@Immutable
data class StageRailBadge(val description: String, val onClick: () -> Unit)

/** The sound disc at the foot of the rail: whose face spins in it, and where a tap goes. */
@Immutable
data class StageRailSound(
    val name: String,
    val seed: String,
    val imageUrl: String? = null,
    /** "Sound: <title>" — what a screen reader says. */
    val description: String,
    val onClick: () -> Unit,
)

/**
 * The right-hand rail, top to bottom: the author's avatar with its "+",
 * every control with its label under it, anything the surface adds
 * ([extra] — Reels' mute), and the sound disc at the foot.
 */
@Composable
fun StageRail(
    author: StageRailAuthor?,
    controls: List<StageRailControl>,
    sound: StageRailSound?,
    modifier: Modifier = Modifier,
    tagPrefix: String = "stage_rail",
    extra: @Composable ColumnScope.() -> Unit = {},
) {
    Column(
        modifier = modifier.testTag(tagPrefix),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(RAIL_GAP),
    ) {
        if (author != null) StageRailAvatar(author = author, tagPrefix = tagPrefix)
        controls.forEach { control ->
            StageRailButton(
                icon = control.icon,
                description = control.description,
                label = control.label,
                tint = control.tint ?: UsTheme.extended.onMedia,
                onClick = control.onClick,
                modifier = Modifier.testTag("$tagPrefix:${control.key}"),
            )
        }
        extra()
        if (sound != null) StageSoundDisc(sound = sound, tagPrefix = tagPrefix)
    }
}

/**
 * A 28dp glyph over a one-line label. The label is what the control says
 * about itself — the count, or its name — and the description is what a
 * screen reader says, which folds the two into one phrase.
 */
@Composable
fun StageRailButton(
    icon: ImageVector,
    description: String,
    label: String?,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    tint: Color = UsTheme.extended.onMedia,
) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = modifier
            .stagePressScale(onClick)
            .sizeIn(minWidth = RAIL_TARGET, minHeight = RAIL_TARGET)
            .clearAndSetSemantics {
                contentDescription = stageControlDescription(description, label)
                role = Role.Button
            },
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Icon(imageVector = icon, contentDescription = null, tint = tint, modifier = Modifier.size(RAIL_ICON))
        if (label != null) {
            Text(
                text = label,
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.onMedia,
                maxLines = 1,
            )
        }
    }
}

/**
 * A rail label: the compact count, zero included; the control's own name
 * only when no count was sent (2026-10-19). Pure.
 */
fun stageCountLabel(count: Int?, noun: String): String = count?.let { formatCount(it) } ?: noun

/** What a rail control announces: its name, then its label when that says something more. Pure. */
fun stageControlDescription(description: String, label: String?): String = when (label) {
    null, description -> description
    else -> "$description, $label"
}

@Composable
private fun StageRailAvatar(author: StageRailAuthor, tagPrefix: String) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Box(
            contentAlignment = Alignment.TopCenter,
            // Room under the avatar for the half of the badge that hangs below it.
            modifier = Modifier.padding(bottom = BADGE_SIZE / 2),
        ) {
            val avatar = @Composable {
                UsAvatar(
                    name = author.name,
                    seed = author.seed,
                    imageUrl = author.imageUrl,
                    size = UsAvatarSize.Medium,
                    contentDescription = "${author.name}, profile",
                    modifier = Modifier
                        .border(AVATAR_RIM, UsTheme.extended.onMedia, CircleShape)
                        .clickable(
                            interactionSource = remember { MutableInteractionSource() },
                            indication = null,
                            role = Role.Button,
                            onClick = author.onOpen,
                        )
                        .testTag("$tagPrefix:author"),
                )
            }
            val progress = author.progress
            if (progress != null) StageProgressRing(progress = progress) { avatar() } else avatar()
            author.badge?.let { badge ->
                Box(
                    contentAlignment = Alignment.Center,
                    modifier = Modifier
                        .align(Alignment.BottomCenter)
                        .offset(y = BADGE_SIZE / 2)
                        .size(BADGE_SIZE)
                        .clip(CircleShape)
                        .background(UsTheme.extended.followBadge)
                        .clickable(role = Role.Button, onClickLabel = badge.description, onClick = badge.onClick)
                        .semantics { contentDescription = badge.description }
                        .testTag("$tagPrefix:badge"),
                ) {
                    Icon(
                        imageVector = UsIcons.Create,
                        contentDescription = null,
                        tint = UsTheme.extended.onMedia,
                        modifier = Modifier.size(BADGE_GLYPH),
                    )
                }
            }
        }
        // The "+" says what it does (founder, 2026-10-19: a bare plus on first
        // launch meant nothing to anyone). Its own tap target, the same action.
        author.badge?.let { badge ->
            Text(
                text = badge.description,
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.onMedia,
                maxLines = 1,
                modifier = Modifier
                    .clickable(role = Role.Button, onClick = badge.onClick)
                    .clearAndSetSemantics { }
                    .testTag("$tagPrefix:badge_label"),
            )
        }
    }
}

/**
 * The playhead as a ring around the author's avatar: a faint white track,
 * and the accent arc starting at twelve o'clock and sweeping clockwise as
 * the video plays (Reels, founder 2026-09-05).
 */
@Composable
fun StageProgressRing(progress: Float, modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    val track = UsTheme.extended.mediaTrack
    val played = UsTheme.extended.ctaGradient
    Box(
        modifier = modifier
            .drawBehind {
                val stroke = RING_STROKE.toPx()
                val inset = stroke / 2
                val arcSize = Size(size.width - stroke, size.height - stroke)
                drawArc(
                    color = track,
                    startAngle = RING_START_ANGLE,
                    sweepAngle = FULL_SWEEP,
                    useCenter = false,
                    topLeft = Offset(inset, inset),
                    size = arcSize,
                    style = Stroke(width = stroke),
                )
                if (progress > 0f) {
                    drawArc(
                        brush = played,
                        startAngle = RING_START_ANGLE,
                        sweepAngle = FULL_SWEEP * progress.coerceIn(0f, 1f),
                        useCenter = false,
                        topLeft = Offset(inset, inset),
                        size = arcSize,
                        style = Stroke(width = stroke, cap = StrokeCap.Round),
                    )
                }
            }
            .padding(RING_STROKE + RING_GAP)
            .testTag("stage_progress"),
        contentAlignment = Alignment.Center,
    ) {
        content()
    }
}

/**
 * The sound disc: a dark record with the sound's creator in the middle.
 * It holds still (founder, 2026-10-19: the turning disc read as a moving
 * profile picture). The whole disc is the target.
 */
@Composable
private fun StageSoundDisc(sound: StageRailSound, tagPrefix: String) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .size(DISC_SIZE)
            .clip(CircleShape)
            .background(Brush.radialGradient(listOf(UsTheme.extended.mediaPlate, UsTheme.extended.stage)))
            .border(DISC_RIM, UsTheme.extended.mediaRim, CircleShape)
            .stagePressScale(sound.onClick)
            .clearAndSetSemantics {
                contentDescription = sound.description
                role = Role.Button
            }
            .testTag("$tagPrefix:sound"),
    ) {
        UsAvatar(name = sound.name, seed = sound.seed, imageUrl = sound.imageUrl, size = UsAvatarSize.Small)
    }
}

/**
 * No ripple. Over media the default indication lit a box around each
 * glyph; the press is shown instead by the control dipping to 85% on a
 * spring and springing back.
 */
@Composable
fun Modifier.stagePressScale(onClick: () -> Unit): Modifier {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(
        targetValue = if (pressed) PRESS_SCALE else 1f,
        animationSpec = spring(dampingRatio = Spring.DampingRatioMediumBouncy, stiffness = PRESS_STIFFNESS),
        label = "stagePress",
    )
    return this
        .clickable(interactionSource = interaction, indication = null, onClick = onClick)
        .graphicsLayer {
            scaleX = scale
            scaleY = scale
        }
}

/** What the caption block at the bottom left says. */
@Immutable
data class StageCaptionState(
    /** Keys the caption's "more" state, so it resets when the post changes. */
    val postId: String,
    /** "@handle", or the display name for an account without one. */
    val handle: String,
    /** "3h", "2d" — the post's age; null to leave it out. */
    val date: String? = null,
    /** A title line above the caption (long videos and reels have one); null for none. */
    val title: String? = null,
    val text: String = "",
    /** The author's tags, without `#`. Each is tappable when the surface can open a tag. */
    val hashtags: List<String> = emptyList(),
    /** Small labels the author declared: "Paid partnership", "AI-generated". */
    val labels: List<String> = emptyList(),
    /** The sound line ("Original sound - Asha"); null for none. */
    val soundLabel: String? = null,
)

/**
 * The block at the bottom left: "@name · date", any declared labels, the
 * title, the caption clamped to two lines with "more" that opens it in
 * place, the tags, and the sound line. Text and targets on one column but
 * never interleaved: the name, a tag and the sound line are the only taps.
 */

@Composable
fun StageCaption(
    state: StageCaptionState,
    onOpenAuthor: () -> Unit,
    modifier: Modifier = Modifier,
    /** A tag was tapped; null draws the tags as plain text. */
    onOpenHashtag: ((tag: String) -> Unit)? = null,
    /** The sound line was tapped; null draws it as plain text. */
    onSound: (() -> Unit)? = null,
    /** What sits directly above the caption: a carousel's dots. */
    above: @Composable ColumnScope.() -> Unit = {},
    tagPrefix: String = "stage_caption",
) {
    Column(
        modifier = modifier.testTag(tagPrefix),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        above()
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            Text(
                text = state.handle,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.onMedia,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier
                    .weight(1f, fill = false)
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                        role = Role.Button,
                        onClick = onOpenAuthor,
                    )
                    .testTag("$tagPrefix:author"),
            )
            state.date?.takeIf { it.isNotBlank() }?.let { date ->
                Text(
                    text = "· $date",
                    style = MaterialTheme.typography.labelMedium,
                    color = UsTheme.extended.onMediaMuted,
                    maxLines = 1,
                )
            }
        }
        if (state.labels.isNotEmpty()) {
            FlowRow(
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            ) {
                state.labels.forEach { label -> StageLabel(label, Modifier.testTag("$tagPrefix:label")) }
            }
        }
        state.title?.takeIf { it.isNotBlank() }?.let { title ->
            Text(
                text = title,
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.onMedia,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.testTag("$tagPrefix:title"),
            )
        }
        if (state.text.isNotBlank()) StageCaptionText(postId = state.postId, text = state.text, tagPrefix = tagPrefix)
        if (state.hashtags.isNotEmpty()) {
            FlowRow(
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            ) {
                state.hashtags.forEach { tag ->
                    Text(
                        text = "#" + tag.trim().trimStart('#'),
                        style = MaterialTheme.typography.labelLarge,
                        fontWeight = FontWeight.Bold,
                        color = UsTheme.extended.onMedia,
                        maxLines = 1,
                        modifier = if (onOpenHashtag != null) {
                            Modifier
                                .clickable(
                                    interactionSource = remember { MutableInteractionSource() },
                                    indication = null,
                                    role = Role.Button,
                                ) { onOpenHashtag(tag) }
                                .testTag("$tagPrefix:hashtag:$tag")
                        } else {
                            Modifier
                        },
                    )
                }
            }
        }
        state.soundLabel?.let { label ->
            Row(
                modifier = Modifier
                    .then(
                        if (onSound != null) {
                            Modifier.clickable(
                                interactionSource = remember { MutableInteractionSource() },
                                indication = null,
                                role = Role.Button,
                                onClickLabel = "Open sound",
                                onClick = onSound,
                            )
                        } else {
                            Modifier
                        },
                    )
                    .sizeIn(minHeight = SOUND_LINE_TARGET)
                    .semantics(mergeDescendants = true) { contentDescription = "Sound: $label" }
                    .testTag("$tagPrefix:sound"),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                Icon(
                    imageVector = UsIcons.Music,
                    contentDescription = null,
                    tint = UsTheme.extended.onMedia,
                    modifier = Modifier.size(SOUND_LINE_ICON),
                )
                Text(
                    text = label,
                    style = MaterialTheme.typography.labelLarge,
                    color = UsTheme.extended.onMedia,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

/** The caption, clamped to two lines with "more" that opens it in place and "less" that folds it. */
@Composable
private fun StageCaptionText(postId: String, text: String, tagPrefix: String) {
    var expanded by rememberSaveable(postId) { mutableStateOf(false) }
    var overflowed by remember(postId, text) { mutableStateOf(false) }
    Column(
        modifier = if (expanded) Modifier.sizeIn(maxHeight = EXPANDED_MAX).verticalScroll(rememberScrollState()) else Modifier,
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.onMedia,
            maxLines = if (expanded) Int.MAX_VALUE else STAGE_CAPTION_LINES,
            overflow = TextOverflow.Ellipsis,
            onTextLayout = { if (!expanded) overflowed = it.hasVisualOverflow },
            modifier = Modifier.testTag("$tagPrefix:text"),
        )
    }
    if (overflowed || expanded) {
        Text(
            text = if (expanded) "less" else "more",
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.onMediaMuted,
            modifier = Modifier
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    role = Role.Button,
                ) { expanded = !expanded }
                .testTag("$tagPrefix:toggle"),
        )
    }
}

/** A small declared label on a dark plate: "Paid partnership". */
@Composable
fun StageLabel(text: String, modifier: Modifier = Modifier) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.SemiBold,
        color = UsTheme.extended.onMedia,
        modifier = modifier
            .clip(RoundedCornerShape(UsTheme.radii.pill))
            .background(UsTheme.extended.mediaPlate)
            .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs / 2),
    )
}

/** Transparent at the top, the stage at 70% by the bottom edge, over the lowest [fraction] of the page. */
@Composable
fun StageBottomScrim(modifier: Modifier = Modifier, fraction: Float = SCRIM_FRACTION) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .fillMaxHeight(fraction)
            .background(
                Brush.verticalGradient(listOf(Color.Transparent, UsTheme.extended.stage.copy(alpha = SCRIM_ALPHA))),
            ),
    )
}

/** The stage at 55% under the status bar, fading out downwards: the header's ground. */
@Composable
fun StageTopScrim(modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .fillMaxHeight(TOP_SCRIM_FRACTION)
            .background(
                Brush.verticalGradient(listOf(UsTheme.extended.stage.copy(alpha = TOP_SCRIM_ALPHA), Color.Transparent)),
            ),
    )
}

/** The paused mark: a white play triangle on a soft dark disc, centred over the held frame. */
@Composable
fun StagePausedGlyph(modifier: Modifier = Modifier) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(PAUSE_DISC)
            .clip(CircleShape)
            .background(UsTheme.extended.mediaPlate)
            .semantics { contentDescription = "Paused" }
            .testTag("stage_paused"),
    ) {
        Icon(
            imageVector = UsIcons.Play,
            contentDescription = null,
            tint = UsTheme.extended.onMedia,
            modifier = Modifier.size(PAUSE_GLYPH),
        )
    }
}

/**
 * How a picture fills the stage: CROP when it is already close to the
 * screen's own shape (a portrait photo or a 9:16 video loses only a sliver),
 * FIT otherwise, so a landscape photo is never cut in half. Pure.
 */
fun stageContentScale(mediaAspect: Float, screenAspect: Float = PORTRAIT_SCREEN_ASPECT): ContentScale {
    if (mediaAspect <= 0f || screenAspect <= 0f) return ContentScale.Fit
    val ratio = mediaAspect / screenAspect
    return if (ratio in CROP_LOW..CROP_HIGH) ContentScale.Crop else ContentScale.Fit
}

/**
 * A photo page of the stage: the photo fitted, over the same photo blurred
 * and darkened to fill the bars a non-portrait photo leaves. The blur needs
 * Android 12; below it the fill is the darkened photo, still cropped behind.
 */
@Composable
fun StagePhoto(page: PostCardMediaPage, modifier: Modifier = Modifier, screenAspect: Float = PORTRAIT_SCREEN_ASPECT) {
    val scale = stageContentScale(page.aspectRatio, screenAspect)
    Box(modifier = modifier.fillMaxSize().background(UsTheme.extended.stage), contentAlignment = Alignment.Center) {
        if (page.url != null) {
            if (scale != ContentScale.Crop) {
                AsyncImage(
                    model = page.url,
                    contentDescription = null,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.fillMaxSize().blur(FILL_BLUR),
                )
                Box(Modifier.fillMaxSize().background(UsTheme.extended.scrim))
            }
            AsyncImage(
                model = page.url,
                contentDescription = page.contentDescription,
                contentScale = scale,
                modifier = Modifier.fillMaxSize().testTag("stage_photo"),
            )
        }
    }
}

/**
 * A photo post's pages, full-bleed and swiped sideways. The pager state is
 * the caller's so the dots ([StagePageDots]) can sit above the caption and
 * the "n/N" counter under the header.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun StagePhotoCarousel(
    pages: List<PostCardMediaPage>,
    pagerState: PagerState,
    modifier: Modifier = Modifier,
    screenAspect: Float = PORTRAIT_SCREEN_ASPECT,
) {
    HorizontalPager(
        state = pagerState,
        // The next photo is decoded before it is swiped to.
        beyondViewportPageCount = 1,
        modifier = modifier.fillMaxSize().testTag("stage_carousel"),
        key = { index -> pages.getOrNull(index)?.mediaId ?: index },
    ) { index ->
        pages.getOrNull(index)?.let { StagePhoto(page = it, screenAspect = screenAspect) }
    }
}

/** A carousel's position dots, white over the media; the current one larger. Silent for a screen reader. */
@Composable
fun StagePageDots(pageCount: Int, currentPage: Int, modifier: Modifier = Modifier) {
    Row(
        modifier = modifier.clearAndSetSemantics { }.testTag("stage_dots"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        repeat(pageCount) { index ->
            Box(
                modifier = Modifier
                    .size(if (index == currentPage) DOT_SELECTED else DOT_UNSELECTED)
                    .clip(CircleShape)
                    .background(if (index == currentPage) UsTheme.extended.onMedia else UsTheme.extended.onMediaDim),
            )
        }
    }
}

/** "2/5" on a dark plate. */
@Composable
fun StagePageCounter(current: Int, count: Int, modifier: Modifier = Modifier) {
    Text(
        text = stagePageCounter(current, count),
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.Medium,
        color = UsTheme.extended.onMedia,
        modifier = modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(UsTheme.extended.mediaPlate)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs)
            .testTag("stage_counter"),
    )
}

/** "2/5" for the second of five; a zero-based [current], clamped. Pure. */
fun stagePageCounter(current: Int, count: Int): String {
    if (count <= 0) return "0/0"
    return "${current.coerceIn(0, count - 1) + 1}/$count"
}

/**
 * A text post as a page: the words large and centred on a colour of its own
 * (picked from the design's create palette by [seed], so the same post keeps
 * its colour and neighbours differ), sized down to fit (never below body
 * size; a longer post scrolls). The words stay clear of the action rail on
 * the right and the caption block at the bottom. [footer] is what follows
 * the text — a poll's ballots.
 */
@Composable
fun StageTextCard(
    text: String,
    modifier: Modifier = Modifier,
    seed: String = text,
    contentPadding: androidx.compose.foundation.layout.PaddingValues = androidx.compose.foundation.layout.PaddingValues(0.dp),
    footer: @Composable ColumnScope.() -> Unit = {},
) {
    val typography = MaterialTheme.typography
    val create = UsTheme.extended.create
    val palette = listOf(create.photo, create.reel, create.audio, create.poll, create.article, create.text)
    val swatch = palette[stageTextColourIndex(seed, palette.size)]
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(swatch.brush)
            // A soft dark wash so white words read on every swatch.
            .background(Brush.verticalGradient(listOf(Color.Transparent, UsTheme.extended.stage.copy(alpha = TEXT_WASH_ALPHA))))
            .padding(contentPadding)
            .padding(end = TEXT_RAIL_CLEARANCE, bottom = TEXT_CAPTION_CLEARANCE)
            .testTag("stage_text_card"),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(start = UsTheme.spacing.xxl, end = UsTheme.spacing.s),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
        ) {
            if (text.isNotBlank()) {
                BasicText(
                    text = text,
                    style = stageTextStyle(text.length, typography).copy(
                        color = UsTheme.extended.onMedia,
                        textAlign = TextAlign.Center,
                        fontWeight = FontWeight.SemiBold,
                    ),
                    autoSize = TextAutoSize.StepBased(
                        minFontSize = typography.bodyLarge.fontSize,
                        maxFontSize = stageTextStyle(text.length, typography).fontSize,
                    ),
                    modifier = Modifier.fillMaxWidth().sizeIn(maxHeight = TEXT_CARD_MAX).testTag("stage_text"),
                )
            }
            footer()
        }
    }
}

/** Which of [size] swatches a text post with [seed] wears: stable for a post, spread across posts. Pure. */
fun stageTextColourIndex(seed: String, size: Int): Int {
    if (size <= 0) return 0
    return Math.floorMod(seed.hashCode(), size)
}

/** The largest style a text post of [length] characters starts from: a line is a headline, a page is body. Pure. */
fun stageTextStyle(length: Int, typography: androidx.compose.material3.Typography) = when {
    length <= SHORT_TEXT -> typography.headlineLarge
    length <= MEDIUM_TEXT -> typography.headlineSmall
    length <= LONG_TEXT -> typography.titleLarge
    else -> typography.bodyLarge
}

/** Lines of caption shown before "more". */
const val STAGE_CAPTION_LINES = 2

/** A phone held upright, 9:19.5 — what the stage is shaped like before it is measured. */
const val PORTRAIT_SCREEN_ASPECT = 9f / 19.5f

private const val CROP_LOW = 0.75f
private const val CROP_HIGH = 1.35f
private const val SHORT_TEXT = 80
private const val MEDIUM_TEXT = 200
private const val LONG_TEXT = 500
private const val SCRIM_FRACTION = 0.45f
private const val SCRIM_ALPHA = 0.7f
private const val TOP_SCRIM_FRACTION = 0.16f
private const val TOP_SCRIM_ALPHA = 0.55f
private const val PRESS_SCALE = 0.85f
private const val PRESS_STIFFNESS = 1200f
private const val RING_START_ANGLE = -90f
private const val FULL_SWEEP = 360f
private val RAIL_GAP = 18.dp
private val RAIL_TARGET = 48.dp
private val RAIL_ICON = 30.dp
private val AVATAR_RIM = 1.5.dp
private val BADGE_SIZE = 20.dp
private val BADGE_GLYPH = 14.dp
private val RING_STROKE = 3.dp
private val RING_GAP = 2.dp
private val DISC_SIZE = 44.dp
private val DISC_RIM = 6.dp
private val SOUND_LINE_ICON = 14.dp
private val SOUND_LINE_TARGET = 32.dp
private val PAUSE_DISC = 72.dp
private val PAUSE_GLYPH = 36.dp
private val DOT_SELECTED = 7.dp
private val DOT_UNSELECTED = 5.dp
private val FILL_BLUR = 28.dp
private val EXPANDED_MAX = 240.dp
private val TEXT_CARD_MAX = 520.dp

// The rail is a 48dp target plus its end margin; the caption block is the
// name line, two caption lines and the sound line.
private val TEXT_RAIL_CLEARANCE = 72.dp
private val TEXT_CAPTION_CLEARANCE = 120.dp
private const val TEXT_WASH_ALPHA = 0.45f
