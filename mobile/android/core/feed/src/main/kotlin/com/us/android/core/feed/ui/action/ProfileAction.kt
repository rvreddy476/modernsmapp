package com.us.android.core.feed.ui.action

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.ChannelRepository
import com.us.android.core.feed.data.ChannelState
import com.us.android.core.model.PostAction
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The profile Action button's source (2026-10-23): it lives on the channel
 * (`GET /v1/channels/:ref` → `action`, null when none), not on the profile
 * payload. The owner's comes from `channels/me` — the cached own channel,
 * which carries `url` and `status` and is rewritten by an edit — so a
 * button the owner just saved shows at once.
 */
@HiltViewModel
class ChannelActionViewModel @Inject constructor(
    private val channels: ChannelRepository,
) : ViewModel() {

    val own: StateFlow<ChannelState> = channels.own

    private val _others = MutableStateFlow<Map<String, PostAction?>>(emptyMap())
    val others: StateFlow<Map<String, PostAction?>> = _others.asStateFlow()

    private val asked = HashSet<String>()

    fun ensure(userId: String, isOwner: Boolean) {
        if (isOwner) {
            viewModelScope.launch { channels.ensureLoaded() }
            return
        }
        if (userId.isBlank() || !asked.add(userId)) return
        viewModelScope.launch {
            val action = (channels.channel(userId) as? AppResult.Success)?.data?.action
            _others.update { it + (userId to action) }
        }
    }
}

/**
 * The Action button on a profile, beside Follow and Message: read from the
 * person's channel and drawn by [ProfileActionButton] (a viewer sees only a
 * live one; the owner sees theirs with its status). Nothing while it loads,
 * and nothing when there is none.
 */
@Composable
fun ProfileChannelAction(
    userId: String,
    isOwner: Boolean,
    modifier: Modifier = Modifier,
    viewModel: ChannelActionViewModel = hiltViewModel(key = "channel-action"),
) {
    LaunchedEffect(userId, isOwner) { viewModel.ensure(userId, isOwner) }
    val own by viewModel.own.collectAsStateWithLifecycle()
    val others by viewModel.others.collectAsStateWithLifecycle()
    val action = if (isOwner) own.channelOrNull?.action else others[userId]
    ProfileActionButton(action = action, isOwner = isOwner, modifier = modifier)
}
