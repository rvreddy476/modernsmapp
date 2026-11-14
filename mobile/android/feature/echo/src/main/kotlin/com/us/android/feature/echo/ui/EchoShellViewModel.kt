package com.us.android.feature.echo.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.notifications.data.UnreadBadge
import com.us.android.feature.echo.data.EchoMediaUrls
import com.us.android.feature.echo.data.EchoPeople
import com.us.android.feature.echo.data.EchoProfile
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Echo's frame: the signed-in account for the top bar's avatar and the
 * drawer (name, @handle, Following / Followers), and the Notifications badge.
 */
@HiltViewModel
class EchoShellViewModel @Inject constructor(
    private val people: EchoPeople,
    private val media: EchoMediaUrls,
    private val badge: UnreadBadge,
) : ViewModel() {

    private val _me = MutableStateFlow<EchoProfile?>(null)
    val me: StateFlow<EchoProfile?> = _me.asStateFlow()

    val mediaUrls: StateFlow<Map<String, String>> = media.urls
    val unread: StateFlow<Int> = badge.count

    val viewerId: String get() = people.viewerId

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            val own = people.ownProfile()
            if (own is AppResult.Success) {
                _me.value = own.data
                media.loadIds(listOfNotNull(own.data.avatarMediaId))
            }
        }
        viewModelScope.launch { badge.refresh() }
    }
}
