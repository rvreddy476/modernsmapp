package com.us.android.feature.dating.safety

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.home.parseInstant
import com.us.android.feature.dating.home.rememberNow
import com.us.android.feature.dating.location.Coordinates
import com.us.android.feature.dating.network.SafetyAlertDto
import com.us.android.feature.dating.photos.DatingPhotoUrls
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.DatingPhoto
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import com.us.android.feature.dating.ui.Tone
import com.us.android.feature.dating.ui.toneColor
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.util.UUID
import javax.inject.Inject

/*
 * A trusted contact's panic alert (dating Android audit H1).
 *
 * When a member presses panic, each of their trusted contacts gets a
 * `dating.safety.panic.trusted_contact` push whose lock-screen copy names
 * nobody and whose deep link carries ONLY the alert id
 * (`/dating/safety/alerts/<id>`). This screen reads the alert from
 * `GET /v1/dating/safety/alerts/:id`, which answers only to a current trusted
 * contact, only inside the alert window, and carries the exact point only
 * when the member consented to send it to THIS contact. Who needs help and
 * where are therefore shown only after the phone is unlocked and the app opened.
 */

/**
 * A panic alert in display terms. [location] is the member's exact point,
 * only with their consent for this contact.
 */
data class PanicAlertUi(
    val alertId: String,
    /** Their first name; null when the contact may no longer see them. */
    val name: String?,
    val photoUrl: String?,
    val triggeredAt: Instant?,
    val status: String,
    val location: Coordinates?,
)

sealed interface PanicAlertState {
    data object Loading : PanicAlertState

    data class Loaded(val alert: PanicAlertUi) : PanicAlertState

    /** Not an alert this account may open: no such alert, not a recipient, or a malformed link. */
    data object NotFound : PanicAlertState

    /** 410: the alert window has ended. */
    data object Expired : PanicAlertState

    /** A read that may work on a retry (the network, a 5xx). */
    data class Failed(val message: String) : PanicAlertState
}

/** The panic alert's words. Pure, so the tests can read them. */
object PanicAlertCopy {
    const val SCREEN_TITLE = "Safety alert"
    const val SOMEONE = "Someone who trusts you"
    const val CALL = "If you think they're in danger, call 112 now."
    const val LOCATION_SHARED = "This is where they were when they pressed it."
    const val LOCATION_NOT_SHARED = "They haven't chosen to send you their location."
    const val OPEN_IN_MAPS = "Open in maps"
    const val REFRESH = "Refresh"
    const val NOT_FOUND_TITLE = "This alert isn't available"
    const val NOT_FOUND_BODY = "It may have been for someone else's account, or you're no longer their trusted contact."
    const val EXPIRED_TITLE = "This alert has ended"
    const val EXPIRED_BODY = "Alerts can be opened for a limited time. " +
        "If you're still worried about them, reach out to them directly."
    const val ACKNOWLEDGED = "Our safety team is on it."
    const val RESOLVED = "Our safety team has marked this alert resolved."

    fun title(name: String?) = "${name ?: SOMEONE} needs help"

    /** "They pressed their safety alert at 19:42 (12 min ago)." */
    fun triggered(at: Instant?, now: Instant, zone: ZoneId): String =
        if (at == null) {
            "They pressed their safety alert."
        } else {
            "They pressed their safety alert at ${clockAndAgo(at, now, zone)}."
        }

    fun status(status: String): String? = when (status) {
        STATUS_ACKNOWLEDGED -> ACKNOWLEDGED
        STATUS_RESOLVED -> RESOLVED
        else -> null
    }

    const val STATUS_ACKNOWLEDGED = "acknowledged"
    const val STATUS_RESOLVED = "resolved"
}

/** Latitude and longitude the maps app can be given: finite and on the globe. Anything else is no location. */
internal fun boundedCoordinates(latitude: Double?, longitude: Double?): Coordinates? {
    if (latitude == null || longitude == null) return null
    if (!latitude.isFinite() || !longitude.isFinite()) return null
    if (latitude !in -MAX_LATITUDE..MAX_LATITUDE || longitude !in -MAX_LONGITUDE..MAX_LONGITUDE) return null
    return Coordinates(latitude, longitude)
}

private const val MAX_LATITUDE = 90.0
private const val MAX_LONGITUDE = 180.0

/** An alert id is a UUID; anything else never reaches the server. Pure. */
internal fun validAlertId(raw: String?): String? {
    val text = raw?.trim()?.takeIf { it.isNotEmpty() } ?: return null
    // Canonical form only (UUID.fromString also takes "1-2-3-4-5"), returned lower-case.
    return runCatching { UUID.fromString(text) }.getOrNull()?.toString()?.takeIf { it.equals(text, ignoreCase = true) }
}

/**
 * The alert as this contact may see it. The location shows only when the
 * server says it was shared AND sent a point on the globe; a resolved alert
 * shows none (the server already withholds it then).
 */
internal fun SafetyAlertDto.toUi(urls: DatingPhotoUrls): PanicAlertUi = PanicAlertUi(
    alertId = alertId,
    name = person?.firstName?.trim()?.takeIf { it.isNotEmpty() },
    photoUrl = urls.forPerson(person),
    triggeredAt = parseInstant(triggeredAt),
    status = status,
    location = if (locationShared && status != PanicAlertCopy.STATUS_RESOLVED) {
        boundedCoordinates(location?.latitude, location?.longitude)
    } else {
        null
    },
)

@HiltViewModel
class PanicAlertViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DatingRepository,
    private val urls: DatingPhotoUrls,
) : ViewModel() {

    private val alertId: String? = validAlertId(savedStateHandle.get<String>(ARG_ALERT_ID))

    private val _state = MutableStateFlow<PanicAlertState>(PanicAlertState.Loading)
    val state: StateFlow<PanicAlertState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        val id = alertId
        if (id == null) {
            _state.value = PanicAlertState.NotFound
            return
        }
        viewModelScope.launch {
            _state.value = when (val result = repository.safetyAlert(id)) {
                is DatingResult.Success -> PanicAlertState.Loaded(result.value.toUi(urls))
                is DatingResult.Failure -> failure(result.error)
            }
        }
    }

    private fun failure(error: DatingError): PanicAlertState = when {
        error.code == CODE_EXPIRED -> PanicAlertState.Expired
        error.code == CODE_NOT_FOUND || error == DatingError.NotAvailable -> PanicAlertState.NotFound
        error is DatingError.Refused && error.status == HTTP_NOT_FOUND -> PanicAlertState.NotFound
        else -> PanicAlertState.Failed(DatingCopy.forError(error))
    }

    companion object {
        const val ARG_ALERT_ID = "alertId"
        private const val CODE_NOT_FOUND = "SAFETY_ALERT_NOT_FOUND"
        private const val CODE_EXPIRED = "SAFETY_ALERT_EXPIRED"
        private const val HTTP_NOT_FOUND = 404
    }
}

/** A trusted contact's view of a panic: who, when, and — only with their consent — where. */
@Composable
fun PanicAlertScreen(onBack: () -> Unit, viewModel: PanicAlertViewModel = hiltViewModel()) {
    // Someone's whereabouts and photo.
    ProtectThisScreen()
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val zone = remember { ZoneId.systemDefault() }
    val now by rememberNow()
    DatingScreen(title = PanicAlertCopy.SCREEN_TITLE, onBack = onBack) { padding ->
        when (val s = state) {
            PanicAlertState.Loading -> LoadingPane()
            PanicAlertState.NotFound -> MessagePane(
                title = PanicAlertCopy.NOT_FOUND_TITLE,
                body = PanicAlertCopy.NOT_FOUND_BODY,
                primaryLabel = "Back",
                onPrimary = onBack,
            )
            PanicAlertState.Expired -> MessagePane(
                title = PanicAlertCopy.EXPIRED_TITLE,
                body = PanicAlertCopy.EXPIRED_BODY,
                primaryLabel = "Back",
                onPrimary = onBack,
            )
            is PanicAlertState.Failed -> MessagePane(
                title = "The alert didn't load",
                body = s.message,
                primaryLabel = "Try again",
                onPrimary = viewModel::refresh,
                extra = { InfoNote(PanicAlertCopy.CALL, tone = Tone.Danger) },
            )
            is PanicAlertState.Loaded -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(top = padding.calculateTopPadding(), bottom = padding.calculateBottomPadding())
                    .padding(horizontal = UsTheme.spacing.pageHorizontal),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                WhoCard(s.alert, PanicAlertCopy.triggered(s.alert.triggeredAt, now, zone))
                WhereCard(
                    point = s.alert.location,
                    onOpenMaps = { point ->
                        val at = "${point.latitude},${point.longitude}"
                        val uri = Uri.parse("geo:$at?q=$at")
                        runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, uri)) }
                    },
                    onRefresh = viewModel::refresh,
                )
            }
        }
    }
}

@Composable
private fun WhoCard(alert: PanicAlertUi, triggered: String) {
    DatingCard {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            DatingPhoto(
                url = alert.photoUrl,
                contentDescription = alert.name,
                modifier = Modifier.size(AVATAR).clip(CircleShape),
            )
            Text(
                PanicAlertCopy.title(alert.name),
                style = MaterialTheme.typography.titleLarge,
                color = UsTheme.extended.textPrimary,
            )
        }
        Text(triggered, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
        PanicAlertCopy.status(alert.status)?.let {
            Text(it, style = MaterialTheme.typography.bodyMedium, color = toneColor(Tone.Positive))
        }
        InfoNote(PanicAlertCopy.CALL, tone = Tone.Danger)
    }
}

@Composable
private fun WhereCard(point: Coordinates?, onOpenMaps: (Coordinates) -> Unit, onRefresh: () -> Unit) {
    DatingCard {
        if (point == null) {
            Text(
                PanicAlertCopy.LOCATION_NOT_SHARED,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
        } else {
            Text(
                PanicAlertCopy.LOCATION_SHARED,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
            UsButton(
                text = PanicAlertCopy.OPEN_IN_MAPS,
                onClick = { onOpenMaps(point) },
                modifier = Modifier.fillMaxWidth(),
            )
        }
        UsSecondaryButton(
            text = PanicAlertCopy.REFRESH,
            onClick = onRefresh,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

private val AVATAR = 56.dp
