package com.us.android.feature.post.composer

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.post.data.dto.VISIBILITY_FOLLOWERS
import com.us.android.feature.post.data.dto.VISIBILITY_PRIVATE
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC

/**
 * Write a post.
 *
 * Renders [ComposerUiState] and calls back. It performs no network, database or
 * file work and keeps no parallel copy of upload or publish truth — the one
 * state object is the only truth, which is what stops the screen showing
 * "uploading" for an upload that already failed.
 *
 * ## IT IS A CANVAS, NOT A FORM
 *
 * This screen used to be a vertical stack of labelled inputs: a bordered field
 * captioned "Post", a full-width "Add photo" button, a captioned "Language"
 * text input, a checkbox, and a submit button at the bottom. Every element
 * announced itself as an input to be completed. Writing a post felt like
 * filling in a record.
 *
 * The rebuild inverts that. The text has no label, no border and no box — it is
 * simply the page, in a size you would actually want to write in. The photo
 * becomes a large card rather than a button. The controls that are not writing
 * — photo, language, length — retreat into a quiet bottom bar as icons. The
 * primary action moves into the top bar as a compact pill, so the canvas is
 * uninterrupted and the commit gesture sits where the thumb expects it.
 *
 * ## AUDIENCE IS CHOSEN HERE
 *
 * The chip beside the avatar is a dropdown: Public, Friends only
 * (`followers` on the wire), Private. It became interactive 2026-09-01,
 * when post-service grew the read-path enforcement (direct-link gate,
 * profile-grid filter) to match the engagement/feed/repost gates — before
 * that, offering a choice nothing honoured would have been a false promise,
 * and people decide what to post based on it.
 */
@Composable
fun ComposerScreen(
    onClose: () -> Unit,
    onPublished: (postId: String) -> Unit,
    viewModel: ComposerViewModel = hiltViewModel(),
    mode: ComposerMode = ComposerMode.Post,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val focusRequester = remember { FocusRequester() }

    // The shape is decided on the Create sheet and fixed here; the ViewModel
    // learns it once so the request builder knows whether to send a title.
    LaunchedEffect(mode) { viewModel.onModeChanged(mode) }

    // Navigation happens on the SERVER's id, once, after the create returned.
    LaunchedEffect(state.phase) {
        (state.phase as? ComposerPhase.Published)?.let { onPublished(it.postId) }
    }

    // LEAVE ONLY ONCE THE DRAFT IS DURABLY GONE (C-CLB-2).
    //
    // The confirm button used to call `onDiscardConfirmed()` and `onClose()`
    // on the same tap. Popping the destination clears the navigation-owned
    // ViewModel and cancels its scope, so the Room delete raced the pop and
    // often lost — content the user explicitly discarded came back the next
    // time they opened the composer.
    LaunchedEffect(state.discarded) {
        if (state.discarded) onClose()
    }

    // The cursor belongs in the text the moment the screen opens. A composer
    // that requires a tap before it will accept typing costs a gesture on every
    // single use, and it is the first thing that makes a writing surface feel
    // sluggish. Restored drafts focus too — the caret lands at the end.
    LaunchedEffect(Unit) {
        if (!state.restoredFromDraft || state.text.isNotEmpty()) {
            runCatching { focusRequester.requestFocus() }
        }
    }

    // SYSTEM BACK GOES THROUGH THE SAME DISCARD DECISION (C-P0-3).
    BackHandler(enabled = state.hasContent && !state.confirmingDiscard) {
        viewModel.onDiscardRequested()
    }

    UsScaffold(
        topBar = {
            ComposerTopBar(
                state = state,
                title = mode.title,
                onClose = { viewModel.onDiscardRequested() },
                onPost = viewModel::onPostPressed,
            )
        },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            Column(
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth()
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = UsTheme.spacing.pageHorizontal),
            ) {
                AuthorHeader(
                    visibility = state.visibility,
                    onVisibilityChanged = viewModel::onVisibilityChanged,
                )

                if (mode == ComposerMode.Article) {
                    TitleField(
                        title = state.title,
                        onTitleChanged = viewModel::onTitleChanged,
                        focusRequester = focusRequester,
                    )
                }

                PostCanvas(
                    text = state.text,
                    onTextChanged = viewModel::onTextChanged,
                    // The article's body takes focus second: the title is the
                    // first thing an article needs, so the cursor starts there.
                    focusRequester = if (mode == ComposerMode.Article) null else focusRequester,
                    longForm = mode == ComposerMode.Article,
                )

                if (state.hasImage) {
                    Spacer(Modifier.height(UsTheme.spacing.m))
                    ImageCard(
                        state = state,
                        onRemove = viewModel::onImageRemoved,
                        onAltTextChanged = viewModel::onAltTextChanged,
                        onDecorativeChanged = viewModel::onDecorativeChanged,
                    )
                }

                ProgressAndErrors(state = state, onRetry = viewModel::onRetry)

                Spacer(Modifier.height(UsTheme.spacing.xl))
            }

            ComposerBottomBar(
                state = state,
                onLanguageChanged = viewModel::onLanguageChanged,
            )
        }

        if (state.confirmingDiscard) {
            DiscardConfirmation(
                // NOT onClose() here. See the discarded LaunchedEffect above.
                onConfirm = viewModel::onDiscardConfirmed,
                onCancel = viewModel::onDiscardCancelled,
            )
        }
    }
}

/**
 * Close on the left, the commit action on the right.
 *
 * The primary action lives here rather than at the bottom of a scrolling column
 * for two reasons: the canvas stays uninterrupted, and the action stops moving.
 * A submit button that sits after the content drifts down the screen as the post
 * grows, so the target is in a different place every time.
 *
 * There is NO plus here anymore. The Create hub's footer rail is the one place
 * that switches what you are making (Text / Image / Reel / Poll); this screen
 * is the Text surface and carries nothing but writing and Post.
 *
 * A cross, not a back arrow — leaving decides the fate of a draft, and an arrow
 * implies the work is simply being set aside.
 */
@Composable
private fun ComposerTopBar(
    state: ComposerUiState,
    title: String,
    onClose: () -> Unit,
    onPost: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.s,
            ),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier
                .size(TAP_TARGET)
                .clip(CircleShape)
                .clickable(onClick = onClose)
                .semantics { contentDescription = "Close" },
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = UsIcons.Close,
                contentDescription = null,
                tint = UsTheme.extended.textPrimary,
            )
        }

        Spacer(Modifier.width(UsTheme.spacing.s))

        // The type name, per the Create sheet's tile — Outfit Bold 17.
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium.copy(fontSize = TOP_BAR_TITLE_SIZE),
            color = UsTheme.extended.textPrimary,
        )

        Spacer(Modifier.weight(1f))

        PostAction(state = state, onPost = onPost)
    }
}

/**
 * The article's headline: a large, borderless single line above the body —
 * the same canvas idea as [PostCanvas], one size up. Required; the reducer
 * blocks Post without it.
 */
@Composable
private fun TitleField(
    title: String,
    onTitleChanged: (String) -> Unit,
    focusRequester: FocusRequester,
) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(bottom = UsTheme.spacing.m),
    ) {
        if (title.isEmpty()) {
            Text(
                text = "Title",
                style = MaterialTheme.typography.titleLarge,
                fontSize = TITLE_TEXT_SIZE,
                color = UsTheme.extended.textDim,
            )
        }
        BasicTextField(
            value = title,
            onValueChange = { onTitleChanged(it.replace('\n', ' ')) },
            singleLine = true,
            textStyle = MaterialTheme.typography.titleLarge.copy(
                fontSize = TITLE_TEXT_SIZE,
                color = UsTheme.extended.textPrimary,
            ),
            cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
            modifier = Modifier
                .fillMaxWidth()
                .focusRequester(focusRequester)
                .testTag("composer-title")
                .semantics { contentDescription = "Article title" },
        )
    }
}

/**
 * Who is posting, and to whom.
 *
 * The avatar is not decoration: it answers "which account am I about to publish
 * as", which matters the moment anyone has more than one. The audience sits
 * beside it as a chip rather than a line of muted body text, because it is a
 * property of the post and should read as one.
 */
@Composable
private fun AuthorHeader(visibility: String, onVisibilityChanged: (String) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = UsTheme.spacing.s, bottom = UsTheme.spacing.m),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsAvatar(name = "You", size = UsAvatarSize.Medium, contentDescription = null)
        AudienceChip(visibility = visibility, onChange = onVisibilityChanged)
    }
}

/** One audience the dropdown offers, and how the chip announces it. */
private data class AudienceOption(
    val value: String,
    val label: String,
    val detail: String,
)

private val AUDIENCE_OPTIONS = listOf(
    AudienceOption(VISIBILITY_PUBLIC, "Public", "Everyone can see this post"),
    AudienceOption(VISIBILITY_FOLLOWERS, "Friends only", "Only people who follow you"),
    AudienceOption(VISIBILITY_PRIVATE, "Private", "Only you"),
)

/**
 * The audience dropdown. The dot is the at-a-glance state — green speaks,
 * gold narrows, muted is only you — and the caret is what marks the chip as
 * a control rather than a fact.
 */
@Composable
private fun AudienceChip(visibility: String, onChange: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val current = AUDIENCE_OPTIONS.firstOrNull { it.value == visibility }
        ?: AUDIENCE_OPTIONS.first()

    Box {
        Row(
            modifier = Modifier
                .clip(RoundedCornerShape(UsTheme.radii.full))
                .background(UsTheme.extended.glassBg)
                .border(
                    width = HAIRLINE,
                    color = UsTheme.extended.glassBorder,
                    shape = RoundedCornerShape(UsTheme.radii.full),
                )
                .clickable { open = true }
                .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs)
                .testTag("composer-audience")
                .clearAndSetSemantics {
                    contentDescription =
                        "Audience: ${current.label}. ${current.detail}. Tap to change."
                    role = Role.DropdownList
                },
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        ) {
            Box(
                modifier = Modifier
                    .size(DOT)
                    .clip(CircleShape)
                    .background(audienceDot(current.value)),
            )
            Text(
                text = current.label,
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textSecondary,
            )
            Text(
                text = "▾",
                style = MaterialTheme.typography.labelMedium,
                color = UsTheme.extended.textMuted,
            )
        }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            AUDIENCE_OPTIONS.forEach { option ->
                DropdownMenuItem(
                    text = {
                        Column {
                            Text(
                                option.label,
                                style = MaterialTheme.typography.bodyMedium,
                                fontWeight = FontWeight.SemiBold,
                            )
                            Text(
                                option.detail,
                                style = MaterialTheme.typography.bodySmall,
                                color = UsTheme.extended.textMuted,
                            )
                        }
                    },
                    onClick = {
                        onChange(option.value)
                        open = false
                    },
                    modifier = Modifier.testTag("composer-audience-${option.value}"),
                )
            }
        }
    }
}

@Composable
private fun audienceDot(value: String) = when (value) {
    VISIBILITY_PRIVATE -> UsTheme.extended.textMuted
    VISIBILITY_FOLLOWERS -> UsTheme.extended.statusWarning
    else -> UsTheme.extended.onlineGreen
}

/**
 * The writing surface.
 *
 * `BasicTextField`, not the design system's bordered input: a box around the
 * text makes it a field to be filled in. Removing the chrome and raising the
 * size is most of what separates a composer from a form, and it costs nothing
 * else — the semantics that tests and screen readers rely on are attached
 * explicitly.
 */
@Composable
private fun PostCanvas(
    text: String,
    onTextChanged: (String) -> Unit,
    focusRequester: FocusRequester?,
    longForm: Boolean = false,
) {
    Box(modifier = Modifier.fillMaxWidth()) {
        if (text.isEmpty()) {
            Text(
                text = if (longForm) "Write your article…" else "What's happening?",
                style = MaterialTheme.typography.bodyLarge,
                fontSize = CANVAS_TEXT_SIZE,
                color = UsTheme.extended.textDim,
            )
        }
        BasicTextField(
            value = text,
            onValueChange = onTextChanged,
            // An article opens twelve lines tall so it reads as a page to
            // write on, not a caption box that happens to grow.
            minLines = if (longForm) ARTICLE_MIN_LINES else 1,
            textStyle = MaterialTheme.typography.bodyLarge.copy(
                fontSize = CANVAS_TEXT_SIZE,
                lineHeight = CANVAS_LINE_HEIGHT,
                color = UsTheme.extended.textPrimary,
            ),
            cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = CANVAS_MIN_HEIGHT)
                .then(if (focusRequester != null) Modifier.focusRequester(focusRequester) else Modifier)
                .semantics { contentDescription = "Post text" },
        )
    }
}

/**
 * The attached photo, as a card.
 *
 * Large, rounded and edge-to-edge within the gutter, so the thing being shared
 * is the biggest element on screen rather than a filename beside a button.
 *
 * Remove is a floating control ON the image, which is where the eye already is.
 * The accessibility decision sits on the image too, as a chip in the corner —
 * the pattern people already know from other apps, and one that keeps a
 * required decision visible without a labelled input consuming a row.
 */
@Composable
private fun ImageCard(
    state: ComposerUiState,
    onRemove: () -> Unit,
    onAltTextChanged: (String) -> Unit,
    onDecorativeChanged: (Boolean) -> Unit,
) {
    var describing by remember { mutableStateOf(false) }
    val shape = RoundedCornerShape(UsTheme.radii.large)

    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(IMAGE_ASPECT)
                .clip(shape)
                .background(UsTheme.extended.bgCard)
                .border(HAIRLINE, UsTheme.extended.borderSubtle, shape),
        ) {
            AsyncImage(
                model = state.imageUri,
                contentDescription = null,
                modifier = Modifier.fillMaxSize(),
            )

            // Remove: a scrim-backed circle so it stays legible on a bright
            // photo without a border fighting the image.
            Box(
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(UsTheme.spacing.s)
                    .size(OVERLAY_BUTTON)
                    .clip(CircleShape)
                    .background(SCRIM)
                    .clickable(enabled = !state.isBusy, onClick = onRemove)
                    .semantics { contentDescription = "Remove photo" },
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    imageVector = UsIcons.Close,
                    contentDescription = null,
                    tint = Color.White,
                    modifier = Modifier.size(OVERLAY_ICON),
                )
            }

            // The accessibility state, always visible on the image. Undecided
            // is deliberately loud: it is the one thing standing between the
            // user and publishing, so it should not look like a hint.
            AltChip(
                decided = state.altDecisionMade,
                modifier = Modifier
                    .align(Alignment.BottomStart)
                    .padding(UsTheme.spacing.s),
                onClick = { describing = !describing },
            )
        }

        // Accessibility is REQUIRED, not suggested: an image with neither a
        // description nor a decorative mark cannot be posted. The two are
        // mutually exclusive and the reducer enforces that.

        // The editor opens on demand once decided, but opens BY DEFAULT while
        // the decision is missing. Slice C found this gated on a failed post
        // attempt, which was unreachable: the Post button is DISABLED until the
        // decision exists, so the click that would reveal it could never happen.
        if (describing || !state.altDecisionMade) {
            UsTextField(
                value = state.altText,
                onValueChange = onAltTextChanged,
                label = "Describe this photo",
                placeholder = "For people using a screen reader",
                singleLine = false,
                enabled = !state.decorative && !state.isBusy,
                modifier = Modifier
                    .fillMaxWidth()
                    .semantics { contentDescription = "Photo description" },
            )

            DecorativeToggle(
                checked = state.decorative,
                enabled = !state.isBusy,
                onCheckedChange = onDecorativeChanged,
            )
        }

        // Muted and polite rather than red and assertive: at this point nothing
        // has gone wrong, the requirement simply has not been met yet.
        if (!state.altDecisionMade) {
            RequirementText("Add a description, or mark the photo as decorative.")
        }
    }
}

/** ALT, in the corner of the photo — the pattern people already recognise. */
@Composable
private fun AltChip(decided: Boolean, modifier: Modifier, onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.small)
    Box(
        modifier = modifier
            .clip(shape)
            .background(if (decided) SCRIM else MaterialTheme.colorScheme.error)
            .clickable(onClick = onClick)
            .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs),
    ) {
        Text(
            text = if (decided) "ALT" else "ALT +",
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.Bold,
            color = Color.White,
        )
    }
}

/**
 * A row, not a checkbox.
 *
 * The whole row is the target, so the tap area matches the label rather than a
 * 20dp square beside it. The semantics string is unchanged.
 */
@Composable
private fun DecorativeToggle(
    checked: Boolean,
    enabled: Boolean,
    onCheckedChange: (Boolean) -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .clickable(enabled = enabled) { onCheckedChange(!checked) }
            .padding(UsTheme.spacing.s)
            .semantics { contentDescription = "This photo is decorative" },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Box(
            modifier = Modifier
                .size(CHECK_BOX)
                .clip(RoundedCornerShape(UsTheme.radii.small))
                .background(
                    if (checked) MaterialTheme.colorScheme.primary else Color.Transparent,
                )
                .border(
                    HAIRLINE,
                    if (checked) MaterialTheme.colorScheme.primary else UsTheme.extended.borderMedium,
                    RoundedCornerShape(UsTheme.radii.small),
                ),
        )
        Text(
            text = "This photo is decorative",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
        )
    }
}

/**
 * The quiet row: language, and how much room is left.
 *
 * Nothing here creates — creation moved into the single "+" in the top bar,
 * because two circular buttons drawn with the same plus glyph sat here and
 * read as a duplicated control. What remains are properties of the text.
 */
@Composable
private fun ComposerBottomBar(
    state: ComposerUiState,
    onLanguageChanged: (String) -> Unit,
) {
    Column {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(HAIRLINE)
                .background(UsTheme.extended.borderSubtle),
        )
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(
                    horizontal = UsTheme.spacing.pageHorizontal,
                    vertical = UsTheme.spacing.s,
                ),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            LanguageChip(language = state.language, onChange = onLanguageChanged)

            Spacer(Modifier.weight(1f))

            // The counter appears only as the limit approaches. A permanent
            // counter reads as a target; one that appears near the ceiling
            // reads as a warning.
            if (state.textCodePoints > MAX_TEXT_CODE_POINTS - COUNTER_VISIBLE_WITHIN) {
                Text(
                    text = "${MAX_TEXT_CODE_POINTS - state.textCodePoints}",
                    style = MaterialTheme.typography.labelLarge,
                    fontWeight = FontWeight.SemiBold,
                    color = if (state.textTooLong) {
                        MaterialTheme.colorScheme.error
                    } else {
                        UsTheme.extended.textMuted
                    },
                )
            }
        }
    }
}

/**
 * Language as a compact chip.
 *
 * It was a full-width captioned text input, which gave a rarely-changed field
 * the same visual weight as the post itself. It is still editable — the field
 * expands on tap — but at rest it is two characters.
 */
@Composable
private fun LanguageChip(language: String, onChange: (String) -> Unit) {
    var editing by remember { mutableStateOf(false) }
    val shape = RoundedCornerShape(UsTheme.radii.full)

    if (editing) {
        UsTextField(
            value = language,
            onValueChange = onChange,
            label = "Language",
            placeholder = "en",
            singleLine = true,
            modifier = Modifier
                .width(LANGUAGE_FIELD)
                .semantics { contentDescription = "Post language" },
        )
    } else {
        Box(
            modifier = Modifier
                .clip(shape)
                .background(UsTheme.extended.glassBg)
                .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
                .clickable { editing = true }
                .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs)
                .semantics { contentDescription = "Post language" },
        ) {
            Text(
                text = language.uppercase().ifEmpty { "EN" },
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textSecondary,
            )
        }
    }
}

@Composable
private fun ProgressAndErrors(state: ComposerUiState, onRetry: () -> Unit) {
    when (val phase = state.phase) {
        is ComposerPhase.PreparingImage -> StatusText("Preparing photo…")

        is ComposerPhase.Uploading -> Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(top = UsTheme.spacing.m)
                .semantics(mergeDescendants = true) {
                    liveRegion = LiveRegionMode.Polite
                    contentDescription = "Uploading photo, ${(phase.fraction * PERCENT).toInt()} percent"
                },
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            LinearProgressIndicator(
                progress = { phase.fraction },
                modifier = Modifier.fillMaxWidth().clip(RoundedCornerShape(UsTheme.radii.full)),
            )
        }

        is ComposerPhase.Confirming -> StatusText("Finishing upload…")
        is ComposerPhase.Publishing -> StatusText("Posting…")

        // No success text here. The screen navigates away on Published, and a
        // "Posted!" message rendered before that would be claiming success the
        // server has not confirmed.
        is ComposerPhase.Published -> Unit

        is ComposerPhase.RetryableFailure -> Column(
            modifier = Modifier.padding(top = UsTheme.spacing.m),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            ErrorText(phase.message)
            // Retry sends the IDENTICAL bytes under the IDENTICAL creation key,
            // so a request that did reach the server replays rather than
            // posting twice.
            UsSecondaryButton(text = "Retry", onClick = onRetry, modifier = Modifier.fillMaxWidth())
        }

        // NO Retry control: this failure cannot succeed on a repeat, and a
        // button that is guaranteed to fail reads as a broken app.
        is ComposerPhase.TerminalFailure -> ErrorText(phase.message)

        is ComposerPhase.Editing -> Unit
    }
}

/**
 * The commit action, as a pill.
 *
 * Compact and right-aligned in the top bar. A disabled control always states
 * its reason: "Post, disabled" with no explanation is the most common
 * accessibility failure in a composer.
 */
@Composable
private fun PostAction(state: ComposerUiState, onPost: () -> Unit) {
    // The semantics go on the BUTTON, not a wrapper: the description and the
    // enabled state must be the same node, or a screen reader announces a
    // reason while the control it describes reports a different state.
    UsButton(
        text = "Post",
        onClick = onPost,
        enabled = state.canPost,
        modifier = Modifier.width(POST_PILL_WIDTH).semantics {
            contentDescription = when (state.blockedReason) {
                PostBlockedReason.Empty -> "Post. Unavailable: add text or a photo first."
                PostBlockedReason.TextTooLong -> "Post. Unavailable: your post is too long."
                PostBlockedReason.MissingAltDecision ->
                    "Post. Unavailable: describe the photo or mark it decorative."

                PostBlockedReason.MediaNotReady -> "Post. Unavailable: the photo is still uploading."
                PostBlockedReason.Busy -> "Post. In progress."
                PostBlockedReason.MissingTitle -> "Post. Unavailable: add a title first."
                null -> "Post"
            }
        },
    )
}

/**
 * Discard, as a modal over a scrim.
 *
 * It used to be two buttons appended to the bottom of the scrolling column,
 * which is easy to miss and easy to hit by accident. A decision that destroys
 * work should interrupt.
 */
@Composable
private fun DiscardConfirmation(onConfirm: () -> Unit, onCancel: () -> Unit) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(SCRIM_HEAVY)
            // Swallows taps so the canvas behind cannot be edited while a
            // destructive decision is open.
            .clickable(onClick = onCancel),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            modifier = Modifier
                .padding(UsTheme.spacing.xl)
                .clip(RoundedCornerShape(UsTheme.radii.extraLarge))
                .background(UsTheme.extended.bgCard)
                .border(
                    HAIRLINE,
                    UsTheme.extended.borderSubtle,
                    RoundedCornerShape(UsTheme.radii.extraLarge),
                )
                .padding(UsTheme.spacing.xl),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            Text(
                text = "Discard this post?",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = "You'll lose what you've written.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                UsSecondaryButton(
                    text = "Keep editing",
                    onClick = onCancel,
                    modifier = Modifier.weight(1f),
                )
                UsButton(text = "Discard", onClick = onConfirm, modifier = Modifier.weight(1f))
            }
        }
    }
}

@Composable
private fun StatusText(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
        modifier = Modifier
            .padding(top = UsTheme.spacing.m)
            .semantics { liveRegion = LiveRegionMode.Polite },
    )
}

@Composable
private fun RequirementText(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
        modifier = Modifier
            .fillMaxWidth()
            .semantics { liveRegion = LiveRegionMode.Polite },
    )
}

@Composable
private fun ErrorText(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.error,
        modifier = Modifier
            .fillMaxWidth()
            // Assertive: an error the user must act on should interrupt, not
            // wait for the next natural pause.
            .semantics { liveRegion = LiveRegionMode.Assertive },
    )
}

/** How close to the ceiling the counter appears. */
private const val COUNTER_VISIBLE_WITHIN = 200

/** Fraction-to-percentage conversion for the upload readout. */
private const val PERCENT = 100

private val HAIRLINE = 1.dp
private val TAP_TARGET = 40.dp
private val DOT = 6.dp
private val CHECK_BOX = 20.dp
private val OVERLAY_BUTTON = 32.dp
private val OVERLAY_ICON = 16.dp
private val POST_PILL_WIDTH = 92.dp
private val LANGUAGE_FIELD = 110.dp
private val CANVAS_MIN_HEIGHT = 120.dp
private val CANVAS_TEXT_SIZE = 19.sp
private val CANVAS_LINE_HEIGHT = 27.sp
private val TITLE_TEXT_SIZE = 26.sp
private val TOP_BAR_TITLE_SIZE = 17.sp

/** The article body's opening height, in lines of [CANVAS_LINE_HEIGHT]. */
private const val ARTICLE_MIN_LINES = 12
private const val IMAGE_ASPECT = 4f / 5f

/**
 * Scrims for controls that sit ON a photo.
 *
 * Legible on any image without a border competing with it. The heavier value
 * is for the discard modal, where the surface behind must read as inactive.
 */
private val SCRIM = Color.Black.copy(alpha = SCRIM_ALPHA)
private val SCRIM_HEAVY = Color.Black.copy(alpha = SCRIM_HEAVY_ALPHA)

private const val SCRIM_ALPHA = 0.6f
private const val SCRIM_HEAVY_ALPHA = 0.8f
