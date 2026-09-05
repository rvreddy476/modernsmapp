package com.us.android.feature.chat.ui.community

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.chat.data.CommunityRules
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.chat.ui.home.ChatTogglePill
import com.us.android.feature.chat.ui.home.HeaderGlyph
import com.us.android.feature.chat.ui.home.pressScale

/**
 * The admin composer: an optional title, the body, up to four pictures,
 * an optional event (title, starts, ends, location), Post. Members never
 * see this screen — the page only offers the FAB to `can_post`.
 */
@Composable
fun CommunityPostScreen(
    onPosted: () -> Unit,
    onBack: () -> Unit,
    viewModel: CommunityPostViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val picker = rememberLauncherForActivityResult(
        ActivityResultContracts.PickMultipleVisualMedia(MAX_PICK),
    ) { uris -> viewModel.stagePictures(uris) }
    LaunchedEffect(state.posted) { if (state.posted) onPosted() }

    UsScaffold(
        topBar = { UsTopBar(title = "New update", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
        ) {
            ChatFormField(
                value = state.title,
                onValueChange = viewModel::onTitleChange,
                label = "Title (optional)",
                placeholder = "A headline for this update",
                tag = "community_post_title",
            )
            ChatFormField(
                value = state.body,
                onValueChange = viewModel::onBodyChange,
                label = "Update",
                placeholder = "What's happening?",
                problem = state.bodyProblem,
                counter = "${state.body.length}/${CommunityRules.UPDATE_BODY_MAX}",
                singleLine = false,
                minLines = BODY_LINES,
                tag = "community_post_body",
            )
            PicturesRow(
                pictures = state.pictures,
                onAdd = { picker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) },
                onRemove = viewModel::removePicture,
            )
            EventSection(state = state, viewModel = viewModel)
            FormError(state.error)
            UsButton(
                text = if (state.posting) "Posting…" else "Post",
                onClick = viewModel::post,
                enabled = state.canPost,
                loading = state.posting,
                modifier = Modifier.fillMaxWidth().testTag("community_post_submit"),
            )
        }
    }
}

/** The staged pictures as thumbnails with × on each, and an add square while there is room. */
@Composable
private fun PicturesRow(
    pictures: List<StagedPicture>,
    onAdd: () -> Unit,
    onRemove: (android.net.Uri) -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
        Text(
            text = "Pictures",
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textSecondary,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            pictures.forEach { picture ->
                Box(modifier = Modifier.size(THUMB).clip(RoundedCornerShape(UsTheme.radii.medium))) {
                    AsyncImage(
                        model = picture.uri,
                        contentDescription = null,
                        contentScale = ContentScale.Crop,
                        modifier = Modifier.fillMaxSize().background(UsTheme.extended.bgCard),
                    )
                    if (picture.mediaId == null) {
                        Box(
                            modifier = Modifier
                                .fillMaxSize()
                                .background(
                                    if (picture.failed) {
                                        UsTheme.extended.accentDeep.copy(alpha = SCRIM_ALPHA)
                                    } else {
                                        Color.Black.copy(alpha = SCRIM_ALPHA)
                                    },
                                ),
                            contentAlignment = Alignment.Center,
                        ) {
                            Icon(
                                imageVector = if (picture.failed) UsIcons.Close else UsIcons.Upload,
                                contentDescription = if (picture.failed) "Upload failed" else "Uploading",
                                tint = Color.White,
                            )
                        }
                    }
                    HeaderGlyph(
                        icon = UsIcons.Close,
                        description = "Remove picture",
                        onClick = { onRemove(picture.uri) },
                        size = REMOVE_TARGET,
                        glyph = REMOVE_GLYPH,
                        modifier = Modifier.align(Alignment.TopEnd),
                    )
                }
            }
            if (pictures.size < MAX_PICK) {
                Box(
                    contentAlignment = Alignment.Center,
                    modifier = Modifier
                        .size(THUMB)
                        .background(UsTheme.extended.bgRaised, RoundedCornerShape(UsTheme.radii.medium))
                        .pressScale(onAdd)
                        .testTag("community_post_add_picture"),
                ) {
                    Icon(imageVector = UsIcons.ImagePlus, contentDescription = "Add pictures", tint = Color.White)
                }
            }
        }
    }
}

@Composable
private fun EventSection(state: CommunityPostUiState, viewModel: CommunityPostViewModel) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)
        ) {
            Text(
                text = "Event",
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textSecondary,
                modifier = Modifier.weight(1f),
            )
            ChatTogglePill(
                text = if (state.eventOpen) "Attached" else "Add event",
                selected = state.eventOpen,
                onClick = viewModel::toggleEvent,
                tag = "community_post_event_toggle",
            )
        }
        if (state.eventOpen) {
            ChatFormField(
                value = state.eventTitle,
                onValueChange = viewModel::onEventTitle,
                label = "Event title",
                placeholder = "Sunday ride",
                tag = "community_post_event_title",
            )
            ChatFormField(
                value = state.eventStartsAt,
                onValueChange = viewModel::onEventStartsAt,
                label = "Starts",
                placeholder = "2026-09-12T09:00:00Z",
                counter = "Date and time, e.g. 2026-09-12T09:00:00Z",
                tag = "community_post_event_starts",
            )
            ChatFormField(
                value = state.eventEndsAt,
                onValueChange = viewModel::onEventEndsAt,
                label = "Ends (optional)",
                placeholder = "2026-09-12T12:00:00Z",
                tag = "community_post_event_ends",
            )
            ChatFormField(
                value = state.eventLocation,
                onValueChange = viewModel::onEventLocation,
                label = "Location (optional)",
                placeholder = "Where to meet",
                tag = "community_post_event_location",
            )
            FormError(state.eventProblem?.takeIf { state.eventTitle.isNotEmpty() || state.eventStartsAt.isNotEmpty() })
        }
    }
}

private const val MAX_PICK = 4
private const val BODY_LINES = 4
private val THUMB = 72.dp
private val REMOVE_TARGET = 28.dp
private val REMOVE_GLYPH = 16.dp
private const val SCRIM_ALPHA = 0.55f
