package com.us.android.feature.profile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.profile.data.InboxSettingOptions
import com.us.android.core.profile.data.PrivacySettings
import com.us.android.core.profile.data.PrivacySettingsRepository
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsLinkRow
import com.us.android.core.ui.UsSettingsOption
import com.us.android.core.ui.UsSettingsSection
import com.us.android.core.ui.UsSettingsSelectRow
import com.us.android.core.ui.UsSettingsSwitchRow
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface InboxSettingsUiState {
    data object Loading : InboxSettingsUiState
    data class Error(val message: String) : InboxSettingsUiState
    data class Loaded(
        val settings: PrivacySettings,
        val saving: Boolean = false,
        val saveError: String? = null,
    ) : InboxSettingsUiState
}

/**
 * Settings › Inbox & messaging (chat parity, CONTRACT §7/§9) — and, since DM
 * consent (2026-10-20), the ONE Messages settings screen. Every row
 * commits the moment it is changed, through the same full-snapshot route as
 * Privacy, against the server's CURRENT settings — and then shows what the
 * server answered, which for a strict or minor account may be a clamped
 * value rather than the one tapped.
 */
@HiltViewModel
class InboxSettingsViewModel @Inject constructor(
    private val repository: PrivacySettingsRepository,
) : ViewModel() {
    private val _state = MutableStateFlow<InboxSettingsUiState>(InboxSettingsUiState.Loading)
    val state: StateFlow<InboxSettingsUiState> = _state.asStateFlow()

    init { load() }

    fun load() {
        _state.value = InboxSettingsUiState.Loading
        viewModelScope.launch {
            _state.value = when (val result = repository.get()) {
                is AppResult.Success -> InboxSettingsUiState.Loaded(result.data)
                is AppResult.Failure -> InboxSettingsUiState.Error("Message settings could not be loaded.")
            }
        }
    }

    fun update(edit: (PrivacySettings) -> PrivacySettings) {
        val current = _state.value as? InboxSettingsUiState.Loaded ?: return
        if (current.saving) return
        _state.value = current.copy(settings = edit(current.settings), saving = true, saveError = null)
        viewModelScope.launch {
            val saved = when (val fresh = repository.get()) {
                is AppResult.Success -> repository.save(edit(fresh.data))
                is AppResult.Failure -> fresh
            }
            _state.value = when (saved) {
                is AppResult.Success -> InboxSettingsUiState.Loaded(saved.data)
                is AppResult.Failure -> current.copy(saveError = "That change couldn't be saved.")
            }
        }
    }
}

@Composable
fun InboxSettingsScreen(onBack: () -> Unit, viewModel: InboxSettingsViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(topBar = { UsTopBar("Inbox & messaging", onBack = onBack) }, applyPageGutter = false) { padding ->
        when (val current = state) {
            InboxSettingsUiState.Loading -> UsLoadingState(Modifier.padding(padding), "Loading")
            is InboxSettingsUiState.Error -> UsErrorState(current.message, Modifier.padding(padding), onRetry = viewModel::load)
            is InboxSettingsUiState.Loaded -> InboxSettingsForm(current, viewModel, Modifier.padding(padding))
        }
    }
}

private fun List<InboxSettingOptions.Option>.forRow(current: String) =
    InboxSettingOptions.withCurrent(this, current).map { UsSettingsOption(it.value, it.label) }

@Composable
@Suppress("LongMethod")
private fun InboxSettingsForm(state: InboxSettingsUiState.Loaded, viewModel: InboxSettingsViewModel, modifier: Modifier) {
    val s = state.settings
    val enabled = !state.saving
    var keywordsOpen by remember { mutableStateOf(false) }
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .testTag("inbox_settings"),
    ) {
        state.saveError?.let {
            Text(it, color = UsTheme.extended.statusDanger, style = MaterialTheme.typography.bodySmall)
        }
        MessagesSection(s, enabled, viewModel)
        UsSettingsSection("Groups and calls") {
            UsSettingsSelectRow(
                title = "Who can add you to groups",
                selected = s.whoCanAddToGroups,
                options = InboxSettingOptions.whoCanAddToGroups.forRow(s.whoCanAddToGroups),
                onSelected = { value -> viewModel.update { it.copy(whoCanAddToGroups = value) } },
                enabled = enabled,
            )
            UsSettingsSelectRow(
                title = "Who can call you",
                selected = s.whoCanCall,
                options = InboxSettingOptions.whoCanCall.forRow(s.whoCanCall),
                onSelected = { value -> viewModel.update { it.copy(whoCanCall = value) } },
                enabled = enabled,
            )
        }
        UsSettingsSection("What others see") {
            UsSettingsSelectRow(
                title = "Activity status",
                description = "Who sees when you're active or were recently active.",
                selected = s.whoCanSeeOnlineStatus,
                options = InboxSettingOptions.audience.forRow(s.whoCanSeeOnlineStatus),
                onSelected = { value -> viewModel.update { it.copy(whoCanSeeOnlineStatus = value) } },
                enabled = enabled,
            )
            UsSettingsSelectRow(
                title = "Read receipts",
                description = "Who sees when you've read their message.",
                selected = s.whoCanSeeReadReceipts,
                options = InboxSettingOptions.audience.forRow(s.whoCanSeeReadReceipts),
                onSelected = { value -> viewModel.update { it.copy(whoCanSeeReadReceipts = value) } },
                enabled = enabled,
            )
            UsSettingsSwitchRow(
                title = "Typing indicators",
                description = "Show people when you're typing to them.",
                checked = s.sendTypingIndicators,
                onCheckedChange = { on -> viewModel.update { it.copy(sendTypingIndicators = on) } },
                enabled = enabled,
            )
            UsSettingsSwitchRow(
                title = "Message previews in notifications",
                description = "Show who wrote and what they said in the notification.",
                checked = s.showMessagePreview,
                onCheckedChange = { on -> viewModel.update { it.copy(showMessagePreview = on) } },
                enabled = enabled,
            )
        }
        val safeMode = s.dmSafeMode
        val blur = s.dmSensitiveMediaFilter
        val keywords = s.dmFilteredKeywords
        if (safeMode != null || blur != null || keywords != null) {
            UsSettingsSection("Safety") {
                if (safeMode != null) {
                    UsSettingsSwitchRow(
                        title = "Safe requests",
                        description = "Requests from brand-new accounts, with links, or with words you filter " +
                            "go to a separate Filtered list.",
                        checked = safeMode,
                        onCheckedChange = { on -> viewModel.update { it.copy(dmSafeMode = on) } },
                        enabled = enabled,
                        modifier = Modifier.testTag("inbox_safe_requests"),
                    )
                }
                if (blur != null) {
                    UsSettingsSwitchRow(
                        title = "Blur sensitive media",
                        description = "Photos and videos that may be sensitive stay blurred until you tap. " +
                            "In message requests they're always blurred.",
                        checked = blur,
                        onCheckedChange = { on -> viewModel.update { it.copy(dmSensitiveMediaFilter = on) } },
                        enabled = enabled,
                    )
                }
                if (keywords != null) {
                    UsSettingsLinkRow(
                        title = "Filtered words",
                        description = "Requests with these words are filtered; messages from people you don't know " +
                            "with them are collapsed.",
                        value = if (keywords.isEmpty()) "None" else "${keywords.size}",
                        onClick = { keywordsOpen = true },
                        enabled = enabled,
                        modifier = Modifier.testTag("inbox_filtered_words"),
                    )
                }
            }
        }
        s.chatBubbleStyle?.let { style ->
            UsSettingsSection("Look") {
                UsSettingsSelectRow(
                    title = "Bubble colour",
                    description = "The colour of your own messages, in every chat.",
                    selected = style,
                    options = InboxSettingOptions.bubbleStyles.forRow(style),
                    onSelected = { value -> viewModel.update { it.copy(chatBubbleStyle = value) } },
                    enabled = enabled,
                )
            }
        }
    }
    val keywords = s.dmFilteredKeywords
    if (keywordsOpen && keywords != null) {
        FilteredWordsSheet(
            words = keywords,
            onSave = { next ->
                viewModel.update { it.copy(dmFilteredKeywords = next) }
                keywordsOpen = false
            },
            onDismiss = { keywordsOpen = false },
        )
    }
}

/**
 * DM consent (2026-10-20): the master switch and who can message you — the
 * one place either is set. The four choices each carry their one-line
 * explanation; an under-18 account is offered only People I follow and No
 * one. A retired stored value shows as the choice the server maps it to.
 */
@Composable
private fun MessagesSection(s: PrivacySettings, enabled: Boolean, viewModel: InboxSettingsViewModel) {
    val messagesOn = s.chatAvailability != InboxSettingOptions.CHAT_PAUSED
    val chosen = InboxSettingOptions.shownWhoCanMessage(s.whoCanMessage, minor = s.under18Mode)
    UsSettingsSection("Messages") {
        UsSettingsSwitchRow(
            title = "Messages",
            description = if (messagesOn) {
                "On: people can message you as you choose below."
            } else {
                "Off: no one can start a chat or message you, and you can't send. Your chats stay readable."
            },
            checked = messagesOn,
            onCheckedChange = { on ->
                viewModel.update {
                    it.copy(chatAvailability = if (on) InboxSettingOptions.CHAT_ENABLED else InboxSettingOptions.CHAT_PAUSED)
                }
            },
            enabled = enabled,
            modifier = Modifier.testTag("inbox_messages_switch"),
        )
        Text(
            "Who can message you",
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.padding(top = UsTheme.spacing.l, bottom = UsTheme.spacing.s),
        )
        Column(modifier = Modifier.selectableGroup().testTag("inbox_who_can_message")) {
            InboxSettingOptions.whoCanMessageChoices(minor = s.under18Mode).forEach { option ->
                WhoCanMessageChoice(
                    option = option,
                    selected = option.value == chosen,
                    enabled = enabled && messagesOn,
                    onSelect = { viewModel.update { it.copy(whoCanMessage = option.value) } },
                )
            }
        }
        if (s.under18Mode) {
            Text(
                "Accounts under 18 can only get messages from people they follow.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(vertical = UsTheme.spacing.s),
            )
        }
    }
}

/** One of the four choices: its name, its one line, and a check when chosen. */
@Composable
private fun WhoCanMessageChoice(
    option: InboxSettingOptions.Option,
    selected: Boolean,
    enabled: Boolean,
    onSelect: () -> Unit,
) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .selectable(selected = selected, enabled = enabled, role = Role.RadioButton, onClick = onSelect)
            .padding(vertical = UsTheme.spacing.m)
            .testTag("inbox_who_can_message:${option.value}"),
    ) {
        Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Text(
                option.label,
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal,
                color = if (enabled) UsTheme.extended.textPrimary else UsTheme.extended.textMuted,
            )
            Text(option.description, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        }
        if (selected) {
            Icon(UsIcons.Check, contentDescription = "Selected", tint = UsTheme.extended.textPrimary)
        }
    }
}

/** Add and remove filtered words; Save replaces the whole list (the server's rule). */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun FilteredWordsSheet(words: List<String>, onSave: (List<String>) -> Unit, onDismiss: () -> Unit) {
    var list by remember { mutableStateOf(words) }
    var input by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
    ) {
        Column(
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.l)
                .testTag("filtered_words_sheet"),
        ) {
            Text("Filtered words", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            Text(
                "Up to ${InboxSettingOptions.MAX_KEYWORDS} words or phrases, ${InboxSettingOptions.MAX_KEYWORD_LENGTH} " +
                    "characters each. Capital letters don't matter.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                UsTextField(
                    value = input,
                    onValueChange = {
                        input = it
                        error = null
                    },
                    label = "Add a word",
                    errorText = error,
                    modifier = Modifier.weight(1f).testTag("filtered_word_input"),
                )
            }
            UsButton(
                text = "Add",
                onClick = {
                    val next = InboxSettingOptions.addKeyword(list, input)
                    if (next == null) {
                        error = if (list.size >= InboxSettingOptions.MAX_KEYWORDS) "The list is full." else "1 to 30 characters."
                    } else {
                        list = next
                        input = ""
                    }
                },
                enabled = input.isNotBlank(),
                modifier = Modifier.testTag("filtered_word_add"),
            )
            list.forEach { word ->
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .background(UsTheme.extended.bgCardSolid, RoundedCornerShape(UsTheme.radii.medium))
                        .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s),
                ) {
                    Text(word, modifier = Modifier.weight(1f))
                    Text(
                        "Remove",
                        color = UsTheme.extended.statusDanger,
                        modifier = Modifier.clickable { list = list - word }.testTag("filtered_word_remove:$word"),
                    )
                }
            }
            UsButton(text = "Save", onClick = { onSave(list) }, modifier = Modifier.fillMaxWidth().testTag("filtered_words_save"))
        }
    }
}
