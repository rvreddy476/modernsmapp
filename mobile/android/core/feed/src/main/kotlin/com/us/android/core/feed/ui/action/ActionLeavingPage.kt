package com.us.android.core.feed.ui.action

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.ActionLinks
import com.us.android.core.model.PostAction
import java.util.UUID

/**
 * The tap behind an Action button, for one button: returns what the button
 * calls on a tap, and draws — for THIS button only, so a screen of pills
 * shares one [ActionLinkViewModel] without drawing its page twice — the
 * "You're leaving VChat" page, "This link is no longer available", or the
 * offline note, and hands the url to the browser when it is time.
 */
@Composable
fun rememberActionLinkOpener(viewModel: ActionLinkViewModel = hiltViewModel(key = "action-link")): (PostAction) -> Unit {
    val owner = rememberSaveable { UUID.randomUUID().toString() }
    val state by viewModel.state.collectAsStateWithLifecycle()
    val launch by viewModel.launch.collectAsStateWithLifecycle()
    val context = LocalContext.current
    LaunchedEffect(launch) {
        val (forOwner, url) = launch ?: return@LaunchedEffect
        if (forOwner != owner) return@LaunchedEffect
        viewModel.launched()
        context.openActionTarget(url)
    }
    val mine = state.takeIf { it.owner == owner }
    if (mine != null && mine !is ActionLinkState.Idle) {
        ActionLinkPage(
            state = mine,
            onContinue = viewModel::continueToSite,
            onRetry = viewModel::retry,
            onBack = viewModel::dismiss,
        )
    }
    return { action -> viewModel.open(owner, action) }
}

/**
 * Hands [url] to the phone's browser: a plain ACTION_VIEW marked BROWSABLE,
 * the way the app opens every other web link (Custom Tabs is not in the
 * build, see WebLinkOpener). Only an https link with a host ever leaves
 * ([ActionLinks.safeTarget]); false when nothing can open it. Never throws.
 */
fun Context.openActionTarget(url: String): Boolean {
    val safe = ActionLinks.safeTarget(url) ?: return false
    val intent = Intent(Intent.ACTION_VIEW, Uri.parse(safe))
        .addCategory(Intent.CATEGORY_BROWSABLE)
        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    return try {
        startActivity(intent)
        true
    } catch (_: ActivityNotFoundException) {
        false
    } catch (_: SecurityException) {
        false
    }
}

/** The page itself, full screen over whatever was tapped. */
@Composable
internal fun ActionLinkPage(
    state: ActionLinkState,
    onContinue: () -> Unit,
    onRetry: () -> Unit,
    onBack: () -> Unit,
) {
    Dialog(
        onDismissRequest = onBack,
        properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false),
    ) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(UsTheme.extended.bgCanvas)
                .systemBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .testTag("action_leaving_page"),
            contentAlignment = Alignment.Center,
        ) {
            when (state) {
                is ActionLinkState.Leaving -> LeavingCard(domain = state.target.domain, onContinue = onContinue, onBack = onBack)
                is ActionLinkState.Gone -> PageCard(
                    icon = UsIcons.CircleSlash,
                    title = "This link is no longer available",
                    detail = "The creator may have removed it, or it was taken down.",
                    tag = "action_link_gone",
                ) { UsButton(text = "Go back", onClick = onBack, modifier = Modifier.fillMaxWidth()) }
                is ActionLinkState.Failed -> PageCard(
                    icon = UsIcons.Info,
                    title = "We couldn't open this link",
                    detail = state.message,
                    tag = "action_link_error",
                ) {
                    Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                        UsSecondaryButton(text = "Go back", onClick = onBack, modifier = Modifier.weight(1f))
                        UsButton(text = "Try again", onClick = onRetry, modifier = Modifier.weight(1f))
                    }
                }
                else -> Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    CircularProgressIndicator(color = UsTheme.extended.accentSolid, modifier = Modifier.size(SPINNER))
                    Text(
                        text = "Opening the link…",
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textMuted,
                        modifier = Modifier.padding(top = UsTheme.spacing.m),
                    )
                }
            }
        }
    }
}

@Composable
private fun LeavingCard(domain: String, onContinue: () -> Unit, onBack: () -> Unit) {
    PageCard(
        icon = UsIcons.ShieldCheck,
        title = "You're leaving VChat",
        subtitle = domain,
        detail = "This site isn't part of VChat. Only continue if you trust it, and never share passwords, " +
            "OTPs or payment details because a post asked you to.",
        tag = "action_leaving_interstitial",
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            UsSecondaryButton(
                text = "Go back",
                onClick = onBack,
                modifier = Modifier.weight(1f).testTag("action_leaving_back"),
            )
            UsButton(
                text = "Continue",
                onClick = onContinue,
                modifier = Modifier.weight(1f).testTag("action_leaving_continue"),
            )
        }
    }
}

@Composable
private fun PageCard(
    icon: ImageVector,
    title: String,
    detail: String,
    tag: String,
    subtitle: String? = null,
    actions: @Composable () -> Unit,
) {
    Column(
        modifier = Modifier.widthIn(max = CARD_MAX).fillMaxWidth().testTag(tag),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Box(
            modifier = Modifier.size(ICON_PLATE).background(UsTheme.extended.fillSubtle, CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Icon(imageVector = icon, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(ICON))
        }
        Text(
            text = title,
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
            modifier = Modifier.semantics { heading() },
        )
        if (subtitle != null) {
            Text(
                text = subtitle,
                style = MaterialTheme.typography.titleMedium,
                color = UsTheme.extended.accentStrong,
                textAlign = TextAlign.Center,
                modifier = Modifier.testTag("action_leaving_domain"),
            )
        }
        Text(
            text = detail,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            textAlign = TextAlign.Center,
        )
        Box(modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.s)) { actions() }
    }
}

private val SPINNER = 28.dp
private val CARD_MAX = 420.dp
private val ICON_PLATE = 56.dp
private val ICON = 26.dp
