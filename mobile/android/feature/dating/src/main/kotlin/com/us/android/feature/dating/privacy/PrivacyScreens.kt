package com.us.android.feature.dating.privacy

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.ConsentGate
import com.us.android.feature.dating.ConsentType
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.OnboardingGate
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.valueOrNull
import com.us.android.feature.dating.network.ConsentsDto
import com.us.android.feature.dating.network.DataExportDto
import com.us.android.feature.dating.network.PrivacyDto
import com.us.android.feature.dating.network.PrivacyUpdateRequest
import com.us.android.feature.dating.premium.displayDate
import com.us.android.feature.dating.ui.ConfirmDialog
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.SectionLabel
import com.us.android.feature.dating.ui.Tone
import com.us.android.feature.dating.ui.errorMessage
import com.us.android.feature.dating.ui.listPadding
import com.us.android.feature.dating.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.ResponseBody
import javax.inject.Inject

data class PrivacyUiState(
    val loading: Boolean = true,
    val privacy: PrivacyDto? = null,
    val consents: ConsentsDto? = null,
    val paused: Boolean = false,
    val exports: List<DataExportDto> = emptyList(),
    val busy: Boolean = false,
    val deleted: Boolean = false,
    val message: UsMessage? = null,
)

/**
 * Privacy and data rights: visibility switches, consent withdrawal (which
 * clears the data it covered), pause, a data export the owner downloads, and
 * deleting the dating profile.
 */
@HiltViewModel
class PrivacyViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : ViewModel() {

    private val _state = MutableStateFlow(PrivacyUiState())
    val state: StateFlow<PrivacyUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun refresh() {
        viewModelScope.launch {
            val privacy = repository.privacy().valueOrNull()
            val consents = repository.consents().valueOrNull()?.also(session::setConsents)
            val profile = repository.profile().valueOrNull()
            val exports = repository.exports().valueOrNull().orEmpty()
            _state.update {
                it.copy(
                    loading = false,
                    privacy = privacy,
                    consents = consents,
                    paused = profile?.profileStatus == OnboardingGate.STATUS_PAUSED,
                    exports = exports.sortedByDescending { e -> e.requestedAt },
                )
            }
        }
    }

    fun update(request: PrivacyUpdateRequest) = busy {
        when (val result = repository.updatePrivacy(request)) {
            is DatingResult.Success -> _state.update { it.copy(privacy = result.value) }
            is DatingResult.Failure -> _state.update { it.copy(message = DatingCopy.message(result.error)) }
        }
    }

    /** Withdrawing deletes what the consent covered; granting again is only offered for Echoes. */
    fun setConsent(type: ConsentType, granted: Boolean) = busy {
        when (val result = repository.setConsent(type.wire, granted)) {
            is DatingResult.Success -> {
                session.setConsents(result.value)
                _state.update {
                    it.copy(consents = result.value, message = successMessage(if (granted) "Consent given." else "Consent withdrawn."))
                }
            }
            is DatingResult.Failure -> _state.update { it.copy(message = DatingCopy.message(result.error)) }
        }
    }

    fun setPaused(paused: Boolean) = busy {
        when (val result = repository.setPaused(paused)) {
            is DatingResult.Success -> {
                session.setProfile(result.value)
                _state.update { it.copy(paused = result.value.profileStatus == OnboardingGate.STATUS_PAUSED) }
            }
            is DatingResult.Failure -> _state.update { it.copy(message = DatingCopy.message(result.error)) }
        }
    }

    fun requestExport() = busy {
        when (val result = repository.requestExport()) {
            is DatingResult.Success -> {
                _state.update { it.copy(message = successMessage("We're preparing your data. It'll be ready to download here.")) }
                _state.update { s -> s.copy(exports = repository.exports().valueOrNull().orEmpty().sortedByDescending { it.requestedAt }) }
            }
            is DatingResult.Failure -> _state.update {
                val status = (result.error as? DatingError.Refused)?.status
                it.copy(message = if (status == HTTP_FORBIDDEN) errorMessage("You can request one export every 7 days.") else DatingCopy.message(result.error))
            }
        }
    }

    /** Streams the export into [write] off the main thread. The app keeps no copy. */
    fun download(exportId: String, write: (ResponseBody) -> Unit) = busy {
        val result = withContext(io) { repository.downloadExport(exportId, write) }
        _state.update {
            it.copy(
                message = when (result) {
                    is DatingResult.Success -> successMessage("Your data was saved.")
                    is DatingResult.Failure -> errorMessage("The download didn't work. If the export has expired, request a new one.")
                },
            )
        }
    }

    fun deleteProfile(reason: String?) = busy {
        when (val result = repository.deleteProfile(reason)) {
            is DatingResult.Success -> {
                session.clear()
                _state.update { it.copy(deleted = true) }
            }
            is DatingResult.Failure -> _state.update { it.copy(message = DatingCopy.message(result.error)) }
        }
    }

    private fun busy(block: suspend () -> Unit) {
        if (_state.value.busy) return
        _state.update { it.copy(busy = true) }
        viewModelScope.launch {
            try {
                block()
            } finally {
                _state.update { it.copy(busy = false) }
            }
        }
    }

    private companion object {
        const val HTTP_FORBIDDEN = 403
    }
}

data class BlockedPersonUi(val userId: String, val name: String, val blockedAt: String?)

data class BlocksUiState(
    val loading: Boolean = true,
    val blocked: List<BlockedPersonUi> = emptyList(),
    val busy: Boolean = false,
    val message: UsMessage? = null,
)

/**
 * Who I have blocked, and lifting a block.
 *
 * Unblocking RESTORES NOTHING — no match, no spark, no conversation comes back —
 * and the screen says so before it asks, because the word "unblock" on its own
 * reads like an undo.
 */
@HiltViewModel
class BlocksViewModel @Inject constructor(
    private val repository: DatingRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(BlocksUiState())
    val state: StateFlow<BlocksUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.blocks()) {
                is DatingResult.Success -> _state.update {
                    it.copy(
                        loading = false,
                        blocked = result.value.items.map { row ->
                            BlockedPersonUi(
                                userId = row.userId,
                                name = row.firstName.takeIf { name -> name.isNotBlank() } ?: "Someone you blocked",
                                blockedAt = displayDate(row.blockedAt),
                            )
                        },
                    )
                }
                is DatingResult.Failure -> _state.update { it.copy(loading = false, message = DatingCopy.message(result.error)) }
            }
        }
    }

    fun unblock(userId: String) {
        if (_state.value.busy) return
        _state.update { it.copy(busy = true) }
        viewModelScope.launch {
            when (val result = repository.unblock(userId)) {
                is DatingResult.Success -> {
                    // Idempotent server-side: drop the row either way.
                    _state.update {
                        it.copy(
                            busy = false,
                            blocked = it.blocked.filterNot { row -> row.userId == userId },
                            message = successMessage("Unblocked. Nothing that was ended has come back."),
                        )
                    }
                }
                is DatingResult.Failure -> _state.update { it.copy(busy = false, message = DatingCopy.message(result.error)) }
            }
        }
    }
}

/** The blocked-people list, reached from Privacy. */
@Composable
fun BlocksScreen(onBack: () -> Unit, viewModel: BlocksViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var confirm by remember { mutableStateOf<BlockedPersonUi?>(null) }

    DatingScreen(title = "Blocked people", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        if (state.loading) {
            LoadingPane()
            return@DatingScreen
        }
        LazyColumn(contentPadding = listPadding(padding), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            item {
                DatingCard {
                    if (state.blocked.isEmpty()) {
                        Text("You haven't blocked anyone.", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                    }
                    state.blocked.forEach { person ->
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Column(Modifier.weight(1f)) {
                                Text(person.name, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
                                person.blockedAt?.let {
                                    Text("Blocked $it", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                                }
                            }
                            TextButton(onClick = { confirm = person }, enabled = !state.busy) {
                                Text("Unblock", color = UsTheme.extended.accentSolid)
                            }
                        }
                    }
                    InfoNote(UNBLOCK_RESTORES_NOTHING)
                }
            }
        }
    }

    confirm?.let { person ->
        ConfirmDialog(
            title = "Unblock ${person.name}?",
            body = UNBLOCK_RESTORES_NOTHING,
            confirmLabel = "Unblock",
            onConfirm = {
                viewModel.unblock(person.userId)
                confirm = null
            },
            onDismiss = { confirm = null },
        )
    }
}

/** The one sentence that must be on screen wherever a block can be lifted. */
const val UNBLOCK_RESTORES_NOTHING =
    "Unblocking doesn't bring anything back: your match, chat and sparks with them stay gone. " +
        "You'll simply be able to see each other again."

@Composable
fun PrivacyScreen(
    onBack: () -> Unit,
    onEditPhotos: () -> Unit,
    onEditPrompts: () -> Unit,
    onOpenBlocks: () -> Unit,
    onDeleted: () -> Unit,
    viewModel: PrivacyViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    var withdraw by remember { mutableStateOf<ConsentType?>(null) }
    var confirmDelete by remember { mutableStateOf(false) }
    var pendingExport by remember { mutableStateOf<String?>(null) }
    val saver = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/json")) { uri: Uri? ->
        val exportId = pendingExport
        pendingExport = null
        if (uri != null && exportId != null) {
            viewModel.download(exportId) { body ->
                context.contentResolver.openOutputStream(uri)?.use { out -> body.byteStream().copyTo(out) }
                    ?: error("the chosen file could not be opened")
            }
        }
    }

    LaunchedEffect(state.deleted) { if (state.deleted) onDeleted() }

    DatingScreen(title = "Privacy and data", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        if (state.loading) {
            LoadingPane()
            return@DatingScreen
        }
        LazyColumn(contentPadding = listPadding(padding), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            item { SectionLabel("Visibility") }
            item {
                val privacy = state.privacy ?: PrivacyDto()
                DatingCard {
                    SwitchRow("Incognito", "Only people you spark can see you.", privacy.incognito, !state.busy) {
                        viewModel.update(PrivacyUpdateRequest(incognito = it))
                    }
                    SwitchRow("Hide when I was last active", null, privacy.hideLastActive, !state.busy) {
                        viewModel.update(PrivacyUpdateRequest(hideLastActive = it))
                    }
                    SwitchRow("Show me verified people only", null, privacy.verifiedOnlyFilter, !state.busy) {
                        viewModel.update(PrivacyUpdateRequest(verifiedOnlyFilter = it))
                    }
                    SwitchRow("Blur my photos until we match", "Everyone else sees them blurred.", privacy.blurPhotosUntilMatch, !state.busy) {
                        viewModel.update(PrivacyUpdateRequest(blurPhotosUntilMatch = it))
                    }
                    InfoNote("Your location is always approximate. Others only see a distance range.")
                }
            }

            item { SectionLabel("Blocked people") }
            item {
                DatingCard {
                    Text(
                        "People you've blocked can't see you, and you won't see them.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textMuted,
                    )
                    UsSecondaryButton(text = "Blocked people", onClick = onOpenBlocks, modifier = Modifier.fillMaxWidth())
                }
            }

            item { SectionLabel("Consent") }
            item {
                DatingCard {
                    ConsentType.entries.forEach { type ->
                        val granted = ConsentGate.granted(state.consents, type)
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Column(Modifier.weight(1f)) {
                                Text(consentLabel(type), style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
                                Text(if (granted) "Given" else "Not given", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                            }
                            when {
                                granted -> TextButton(onClick = { withdraw = type }, enabled = !state.busy) { Text("Withdraw", color = UsTheme.extended.statusDanger) }
                                type == ConsentType.ECHOES -> TextButton(onClick = { viewModel.setConsent(type, true) }, enabled = !state.busy) {
                                    Text("Turn on", color = UsTheme.extended.accentSolid)
                                }
                            }
                        }
                    }
                }
            }

            item { SectionLabel("Your profile") }
            item {
                DatingCard {
                    UsSecondaryButton(text = "Edit photos", onClick = onEditPhotos, modifier = Modifier.fillMaxWidth())
                    UsSecondaryButton(text = "Edit prompts", onClick = onEditPrompts, modifier = Modifier.fillMaxWidth())
                    UsSecondaryButton(
                        text = if (state.paused) "Resume dating" else "Pause my profile",
                        enabled = !state.busy,
                        onClick = { viewModel.setPaused(!state.paused) },
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }

            item { SectionLabel("Your data") }
            item {
                DatingCard {
                    Text("Get a copy of your dating data.", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                    state.exports.forEach { export ->
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Column(Modifier.weight(1f)) {
                                Text(exportLabel(export.status), style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
                                displayDate(export.requestedAt)?.let {
                                    Text("Requested $it", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                                }
                            }
                            if (export.status == EXPORT_READY) {
                                TextButton(
                                    enabled = !state.busy,
                                    onClick = {
                                        pendingExport = export.id
                                        saver.launch("momentum-dating-data.json")
                                    },
                                ) { Text("Download", color = UsTheme.extended.accentSolid) }
                            }
                        }
                    }
                    UsSecondaryButton(text = "Request my data", enabled = !state.busy, onClick = viewModel::requestExport, modifier = Modifier.fillMaxWidth())
                }
            }
            item {
                DatingCard {
                    Text("Delete my dating profile", style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.statusDanger)
                    InfoNote("Your dating profile, photos, sparks and matches are removed. Reports about safety are kept as the law requires.", tone = Tone.Danger)
                    UsSecondaryButton(text = "Delete dating profile", enabled = !state.busy, onClick = { confirmDelete = true }, modifier = Modifier.fillMaxWidth())
                }
            }
        }
    }

    withdraw?.let { type ->
        ConfirmDialog(
            title = "Withdraw consent?",
            body = when (type) {
                ConsentType.BIOMETRIC_SELFIE -> "We'll delete your face-check data. You may need to verify again to keep using Dating."
                ConsentType.ECHOES -> "Your Momentum activity will no longer show on your dating profile."
                else -> "We'll delete this information from your dating profile."
            },
            confirmLabel = "Withdraw",
            destructive = true,
            onConfirm = {
                viewModel.setConsent(type, false)
                withdraw = null
            },
            onDismiss = { withdraw = null },
        )
    }
    if (confirmDelete) {
        ConfirmDialog(
            title = "Delete your dating profile?",
            body = "This can't be undone from the app. Your Momentum account stays.",
            confirmLabel = "Delete",
            destructive = true,
            onConfirm = {
                confirmDelete = false
                viewModel.deleteProfile(null)
            },
            onDismiss = { confirmDelete = false },
        )
    }
}

@Composable
private fun SwitchRow(title: String, subtitle: String?, checked: Boolean, enabled: Boolean, onChange: (Boolean) -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
            subtitle?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted) }
        }
        Switch(
            checked = checked,
            onCheckedChange = onChange,
            enabled = enabled,
            colors = SwitchDefaults.colors(checkedTrackColor = UsTheme.extended.accentSolid),
        )
    }
}

fun consentLabel(type: ConsentType): String = when (type) {
    ConsentType.SENSITIVE_RELIGION -> "Religion on my profile"
    ConsentType.SENSITIVE_COMMUNITY -> "Community on my profile"
    ConsentType.BIOMETRIC_SELFIE -> "Face check"
    ConsentType.ECHOES -> "Echoes (Momentum activity)"
}

fun exportLabel(status: String): String = when (status) {
    "pending", "processing" -> "Being prepared"
    EXPORT_READY -> "Ready to download"
    "failed" -> "Couldn't be prepared"
    "expired" -> "Expired"
    else -> status
}

private const val EXPORT_READY = "ready"
