package com.us.android.feature.profile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.PersonalProfile
import com.us.android.core.model.Profile
import com.us.android.core.model.ProfileCounts
import com.us.android.core.model.ProfileRelationship
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsStat
import com.us.android.core.ui.UsStatRow

/**
 * Profile screen — stateful entry point.
 *
 * This composable does exactly two things: collect state and forward events.
 * Everything that renders is a stateless composable below it, which is what
 * makes the screen previewable in every state and screenshot-testable without
 * a ViewModel, a DI graph, or a network fake.
 *
 * [onBack] is null when this screen is a tab root and non-null when it was
 * pushed onto the stack. The top bar renders a back control only in the second
 * case — a tab root with a back arrow sends the user somewhere they did not
 * come from.
 */
@Composable
fun ProfileScreen(
    destinations: ProfileDestinations,
    viewModel: ProfileViewModel = hiltViewModel(),
    startChatViewModel: StartChatViewModel = hiltViewModel(),
    mediaViewModel: ProfileMediaViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val chatState by startChatViewModel.state.collectAsStateWithLifecycle()
    val mediaState by mediaViewModel.state.collectAsStateWithLifecycle()

    val loadedProfile = (state as? ProfileUiState.Content)?.profile
    LaunchedEffect(loadedProfile?.avatarMediaId, loadedProfile?.coverMediaId) {
        loadedProfile?.let { profile ->
            mediaViewModel.bind(profile.avatarMediaId, profile.coverMediaId)
        }
    }

    // The server returns the conversation; the host decides that opening it
    // means pushing the thread. Consumed once and cleared, or the thread would
    // be pushed again every time this screen resumed.
    LaunchedEffect(chatState.openConversation) {
        chatState.openConversation?.let { open ->
            destinations.onOpenChat?.invoke(open.conversationId, open.title)
            startChatViewModel.onConversationOpened()
        }
    }

    ProfileContent(
        state = state,
        destinations = destinations,
        actions = ProfileActions(
            onRetry = viewModel::load,
            onFollowToggle = viewModel::onFollowToggle,
            onBlockToggle = viewModel::onBlockToggle,
            onDismissActionError = viewModel::dismissActionError,
            onMessage = if (destinations.onOpenChat == null) {
                null
            } else {
                { userId, name -> startChatViewModel.open(userId, name) }
            },
            onDismissChatError = startChatViewModel::dismissError,
        ),
        chatState = chatState,
        media = ProfileMediaUrls(mediaState.avatarUrl, mediaState.coverUrl),
    )
}

data class ProfileDestinations(
    val onOpenFollowers: (userId: String) -> Unit,
    val onOpenFollowing: (userId: String) -> Unit,
    val onBack: (() -> Unit)? = null,
    val onEditProfile: (() -> Unit)? = null,
    val onOpenSettings: (() -> Unit)? = null,
    val onOpenChat: ((conversationId: String, title: String) -> Unit)? = null,
)

internal data class ProfileActions(
    val onRetry: () -> Unit,
    val onFollowToggle: () -> Unit,
    val onBlockToggle: () -> Unit,
    val onDismissActionError: () -> Unit,
    val onMessage: ((userId: String, displayName: String) -> Unit)? = null,
    val onDismissChatError: () -> Unit = {},
)

internal data class ProfileMediaUrls(
    val avatar: String? = null,
    val cover: String? = null,
)

/** Stateless renderer. Receives immutable state and callbacks; fetches nothing. */
@Composable
internal fun ProfileContent(
    state: ProfileUiState,
    actions: ProfileActions,
    destinations: ProfileDestinations,
    modifier: Modifier = Modifier,
    chatState: StartChatUiState = StartChatUiState(),
    media: ProfileMediaUrls = ProfileMediaUrls(),
) {
    UsScaffold(
        modifier = modifier,
        // The title tracks the loaded profile. While loading it stays generic
        // rather than flashing a placeholder name that then changes.
        topBar = {
            UsTopBar(
                title = state.title(),
                onBack = destinations.onBack,
                actions = {
                    if (destinations.onOpenSettings != null) {
                        IconButton(onClick = destinations.onOpenSettings) {
                            Icon(UsIcons.Settings, contentDescription = "Settings")
                        }
                    }
                },
            )
        },
        applyPageGutter = false,
    ) { padding ->
        when (state) {
            is ProfileUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading profile",
            )

            is ProfileUiState.Error -> UsErrorState(
                message = state.message,
                modifier = Modifier.padding(padding),
                onRetry = if (state.retryable) actions.onRetry else null,
            )

            is ProfileUiState.Content -> LoadedProfile(
                state = state,
                actions = actions,
                destinations = destinations,
                chatState = chatState,
                media = media,
                modifier = Modifier.padding(padding),
            )
        }
    }
}

private fun ProfileUiState.title(): String = when (this) {
    is ProfileUiState.Content -> if (profile.isOwnProfile) "My profile" else profile.nameForDisplay
    else -> "Profile"
}

@Composable
private fun LoadedProfile(
    state: ProfileUiState.Content,
    actions: ProfileActions,
    destinations: ProfileDestinations,
    chatState: StartChatUiState,
    media: ProfileMediaUrls,
    modifier: Modifier = Modifier,
) {
    val profile = state.profile
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
    ) {
        ProfileHeader(profile = profile, avatarUrl = media.avatar, coverUrl = media.cover)

        UsStatRow(
            stats = listOf(
                UsStat("Posts", state.counts.posts),
                UsStat("Followers", state.counts.followers) { destinations.onOpenFollowers(profile.userId) },
                UsStat("Following", state.counts.following) { destinations.onOpenFollowing(profile.userId) },
            ),
        )

        // Editing is offered only when the host supplied a destination AND the
        // loaded profile is genuinely the viewer's. Two conditions rather than
        // one because the endpoint behind the form replaces the OWNER's fields
        // keyed off the access token — an edit control on someone else's page
        // would silently overwrite the viewer's own profile.
        if (profile.isOwnProfile && destinations.onEditProfile != null) {
            UsSecondaryButton(
                text = "Edit profile",
                onClick = destinations.onEditProfile,
                modifier = Modifier.fillMaxWidth(),
            )
        }

        if (!profile.isOwnProfile) {
            MessageControls(
                profile = profile,
                chatState = chatState,
                onMessage = actions.onMessage,
                onDismissChatError = actions.onDismissChatError,
            )

            RelationshipControls(
                relationship = state.relationship,
                busy = state.relationshipBusy,
                onFollowToggle = actions.onFollowToggle,
                onBlockToggle = actions.onBlockToggle,
            )
        }

        state.actionError?.let { error ->
            // Transient and dismissible: a failed unfollow must not replace a
            // successfully loaded profile with an error screen.
            Text(
                text = error,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier
                    .fillMaxWidth()
                    .semantics { }
                    .padding(bottom = UsTheme.spacing.m),
            )
            UsSecondaryButton(
                text = "Dismiss",
                onClick = actions.onDismissActionError,
                modifier = Modifier.fillMaxWidth(),
            )
        }

        // The post grid is not built here. `/v1/posts/by-author/:id` was not
        // part of the verified capture, and inventing its list DTO is exactly
        // the failure this project keeps paying for.
        UsEmptyState(
            title = "Posts coming soon",
            detail = "This surface lands once the post list contract is captured.",
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

/**
 * Start a conversation with the person whose profile this is.
 *
 * The button is ALWAYS offered when the host has somewhere to open a thread —
 * it is never pre-filtered on a locally known privacy setting. graph-service
 * owns that decision and re-evaluates it on every attempt; a client-side copy
 * would be a second implementation of the privacy matrix that goes stale the
 * moment somebody changes a setting, and stale in whichever direction the last
 * deploy happened to choose. A refusal is answered with a reason instead.
 */
@Composable
private fun MessageControls(
    profile: Profile,
    chatState: StartChatUiState,
    onMessage: ((userId: String, displayName: String) -> Unit)?,
    onDismissChatError: () -> Unit,
) {
    if (onMessage != null) {
        UsSecondaryButton(
            text = "Message",
            onClick = { onMessage(profile.userId, profile.nameForDisplay) },
            enabled = !chatState.busy,
            modifier = Modifier.fillMaxWidth(),
        )
    }

    // A policy refusal is NOT retryable, so it gets no retry control. The
    // answer stays the same until the other person changes their settings, and
    // a button that cannot work reads as a broken app rather than as a
    // boundary somebody set.
    chatState.notAllowed?.let { message ->
        Text(
            text = message,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.fillMaxWidth(),
        )
    }

    chatState.error?.let { message ->
        Text(
            text = message,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier.fillMaxWidth(),
        )
        UsSecondaryButton(
            text = "Dismiss",
            onClick = onDismissChatError,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun ProfileHeader(
    profile: Profile,
    avatarUrl: String?,
    coverUrl: String?,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(top = UsTheme.spacing.xxxxl),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        if (!coverUrl.isNullOrBlank()) {
            AsyncImage(
                model = coverUrl,
                contentDescription = "${profile.nameForDisplay}'s cover photo",
                modifier = Modifier
                    .fillMaxWidth()
                    .height(152.dp)
                    .clip(MaterialTheme.shapes.large),
                contentScale = ContentScale.Crop,
            )
        }
        UsAvatar(
            name = profile.nameForDisplay,
            size = UsAvatarSize.Large,
            seed = profile.userId,
            imageUrl = avatarUrl,
            contentDescription = "${profile.nameForDisplay}'s profile photo",
        )

        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                Text(
                    text = profile.nameForDisplay,
                    style = MaterialTheme.typography.headlineSmall,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.semantics { heading() },
                )
                if (profile.isVerified) {
                    Text(
                        text = "Verified",
                        style = MaterialTheme.typography.labelSmall,
                        color = UsTheme.extended.statusSuccess,
                    )
                }
            }

            profile.subtitle()?.let {
                Text(
                    text = it,
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textSecondary,
                )
            }

            if (profile.bio.isNotBlank()) {
                Text(
                    text = profile.bio,
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.padding(top = UsTheme.spacing.m),
                )
            }
        }
    }
}

/**
 * Profession and location, joined only when both exist.
 *
 * Every string field on this payload can legitimately be empty — `PUT /me`
 * with an empty body clears them — so a naive `"$profession · $location"`
 * renders a bare middle dot for a large share of real accounts.
 */
private fun Profile.subtitle(): String? =
    listOf(profession, location).filter { it.isNotBlank() }.joinToString(" · ").ifBlank { null }

@Composable
private fun RelationshipControls(
    relationship: ProfileRelationship,
    busy: Boolean,
    onFollowToggle: () -> Unit,
    onBlockToggle: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        if (relationship.isBlocked) {
            // A blocked account offers exactly one action. Showing Follow
            // beside Blocked would present a control the server will reject.
            UsSecondaryButton(
                text = "Unblock",
                onClick = onBlockToggle,
                enabled = !busy,
                modifier = Modifier.fillMaxWidth(),
            )
        } else {
            if (relationship.isFollowing) {
                UsSecondaryButton(
                    text = "Following",
                    onClick = onFollowToggle,
                    enabled = !busy,
                    modifier = Modifier.fillMaxWidth(),
                )
            } else {
                UsButton(
                    text = "Follow",
                    onClick = onFollowToggle,
                    loading = busy,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
            UsSecondaryButton(
                text = "Block",
                onClick = onBlockToggle,
                enabled = !busy,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

// ── Previews ────────────────────────────────────────────────────────────
//
// Every state the screen can reach. These are the screenshot-test entry
// points and the reason the renderer is stateless.

private val previewProfile = Profile(
    userId = "03b43cb4-420b-4d2d-baae-2ade11490b19",
    displayName = "Ada Lovelace",
    bio = "Mathematician. Writing notes on the Analytical Engine.",
    category = "personal",
    profession = "Mathematician",
    website = "",
    location = "London",
    badgeFlags = 0,
    isVerified = true,
    verificationLevel = "email",
    statusText = "",
    statusEmoji = "",
    profileThemeColor = "#1A73E8",
    memberSinceBadge = true,
    counts = ProfileCounts(followers = 12_400, following = 318, friends = 12, posts = 42),
    createdAt = "2026-08-16T17:53:05Z",
)

private fun previewState(
    profile: Profile = previewProfile,
    relationship: ProfileRelationship = ProfileRelationship(),
    busy: Boolean = false,
    actionError: String? = null,
) = ProfileUiState.Content(
    profile = profile,
    stats = null,
    relationship = relationship,
    relationshipBusy = busy,
    actionError = actionError,
)

@Composable
private fun PreviewHost(state: ProfileUiState) = UsTheme {
    ProfileContent(
        state = state,
        actions = ProfileActions(
            onRetry = {},
            onFollowToggle = {},
            onBlockToggle = {},
            onDismissActionError = {},
        ),
        destinations = ProfileDestinations({}, {}, onEditProfile = {}),
    )
}

@Preview(name = "Other user — not following", showBackground = true, heightDp = 640)
@Composable
private fun ProfileOtherPreview() = PreviewHost(previewState())

@Preview(name = "Other user — following", showBackground = true, heightDp = 640)
@Composable
private fun ProfileFollowingPreview() =
    PreviewHost(previewState(relationship = ProfileRelationship(isFollowing = true)))

@Preview(name = "Other user — blocked", showBackground = true, heightDp = 640)
@Composable
private fun ProfileBlockedPreview() =
    PreviewHost(previewState(relationship = ProfileRelationship(isBlocked = true)))

@Preview(name = "Own profile", showBackground = true, heightDp = 640)
@Composable
private fun ProfileOwnPreview() = PreviewHost(
    previewState(
        profile = previewProfile.copy(
            personal = PersonalProfile(
                firstName = "Ada",
                lastName = "Lovelace",
                dateOfBirth = "1815-12-10T00:00:00Z",
                gender = "female",
                timezone = "",
                introMediaUrl = "",
                ctaUrl = "",
                updatedAt = "2026-08-16T17:59:08Z",
            ),
        ),
    ),
)

@Preview(name = "Cleared display name", showBackground = true, heightDp = 640)
@Composable
private fun ProfileClearedNamePreview() = PreviewHost(
    previewState(
        profile = previewProfile.copy(displayName = "", profession = "", location = ""),
    ),
)

@Preview(name = "Action failed", showBackground = true, heightDp = 640)
@Composable
private fun ProfileActionErrorPreview() =
    PreviewHost(previewState(actionError = "That didn't go through. Try again."))

@Preview(name = "Loading", showBackground = true, heightDp = 320)
@Composable
private fun ProfileLoadingPreview() = PreviewHost(ProfileUiState.Loading)

@Preview(name = "Error — retryable", showBackground = true, heightDp = 320)
@Composable
private fun ProfileErrorPreview() = PreviewHost(
    ProfileUiState.Error("You're offline. Check your connection and try again.", retryable = true),
)

@Preview(name = "Error — terminal", showBackground = true, heightDp = 320)
@Composable
private fun ProfileErrorTerminalPreview() =
    PreviewHost(ProfileUiState.Error("This profile isn't available.", retryable = false))
