package com.us.android.feature.profile.ui

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.ConnectionStatus
import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.ConnectionRequestDirection
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

class ConnectionsViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private class Session : SessionStateProvider {
        override val sessionState: StateFlow<SessionState> =
            MutableStateFlow(SessionState.Authenticated(userId = "me", sessionId = "s"))
    }

    private fun viewModel(source: FakeConnections, userId: String? = null, tab: String? = null) = ConnectionsViewModel(
        source = source,
        sessionStateProvider = Session(),
        savedStateHandle = SavedStateHandle(
            buildMap {
                userId?.let { put("userId", it) }
                tab?.let { put("tab", it) }
            },
        ),
    )

    @Test
    fun `the viewer's own screen has all three tabs, anyone else's only Connections`() = runTest {
        assertThat(viewModel(FakeConnections()).state.value.tabs).containsExactly(
            ConnectionsTab.CONNECTIONS,
            ConnectionsTab.RECEIVED,
            ConnectionsTab.SENT,
        ).inOrder()
        assertThat(viewModel(FakeConnections(), userId = "other").state.value.tabs).containsExactly(ConnectionsTab.CONNECTIONS)
        // The viewer's own id is their own screen too.
        assertThat(viewModel(FakeConnections(), userId = "me").state.value.isOwn).isTrue()
    }

    @Test
    fun `a request tab asked for on someone else's screen falls back to Connections`() = runTest {
        val source = FakeConnections()
        val vm = viewModel(source, userId = "other", tab = "received")

        assertThat(vm.state.value.selected).isEqualTo(ConnectionsTab.CONNECTIONS)
        assertThat(source.calls).containsExactly("connections(other)")
    }

    @Test
    fun `opening on Requests reads only the received list`() = runTest {
        val source = FakeConnections().apply { received = AppResult.Success(listOf(requestPerson("s-1", "Ana"))) }
        val vm = viewModel(source, tab = "received")

        assertThat(source.calls).containsExactly("requests(${ConnectionRequestDirection.RECEIVED})")
        assertThat(vm.state.value.received.rows.map { it.userId }).containsExactly("s-1")
        assertThat(vm.state.value.received.loaded).isTrue()
    }

    @Test
    fun `accepting a request removes the row and marks Connections stale`() = runTest {
        val source = FakeConnections().apply { received = AppResult.Success(listOf(requestPerson("s-1"), requestPerson("s-2"))) }
        val vm = viewModel(source, tab = "received")

        vm.accept("s-1")

        assertThat(source.calls).contains("accept(s-1)")
        assertThat(vm.state.value.received.rows.map { it.userId }).containsExactly("s-2")
        assertThat(vm.state.value.connections.loaded).isFalse()
        assertThat(vm.state.value.busyIds).isEmpty()
    }

    @Test
    fun `a refused decline keeps the row and marks it failed`() = runTest {
        val source = FakeConnections().apply {
            received = AppResult.Success(listOf(requestPerson("s-1")))
            writeResult = FakeConnections.FAILURE
        }
        val vm = viewModel(source, tab = "received")

        vm.decline("s-1")

        assertThat(vm.state.value.received.rows.map { it.userId }).containsExactly("s-1")
        assertThat(vm.state.value.failedIds).containsExactly("s-1")
    }

    @Test
    fun `cancelling a sent request names the receiver and removes the row`() = runTest {
        val source = FakeConnections().apply { sent = AppResult.Success(listOf(requestPerson("r-1"))) }
        val vm = viewModel(source, tab = "sent")

        vm.cancel("r-1")

        assertThat(source.calls).contains("cancel(r-1)")
        assertThat(vm.state.value.sent.rows).isEmpty()
    }

    @Test
    fun `remove asks first and only the confirmation removes`() = runTest {
        val person = connectionPerson("c-1", "Cy")
        val source = FakeConnections().apply { people = AppResult.Success(listOf(person)) }
        val vm = viewModel(source)

        vm.askRemove(person)
        assertThat(source.calls).doesNotContain("remove(c-1)")
        assertThat(vm.state.value.confirmRemove).isEqualTo(person)

        vm.confirmRemove()

        assertThat(source.calls).contains("remove(c-1)")
        assertThat(vm.state.value.connections.rows).isEmpty()
        assertThat(vm.state.value.confirmRemove).isNull()
    }

    @Test
    fun `nobody can act on someone else's list`() = runTest {
        val person = connectionPerson("c-1")
        val source = FakeConnections().apply { people = AppResult.Success(listOf(person)) }
        val vm = viewModel(source, userId = "other")

        vm.askRemove(person)
        vm.accept("c-1")

        assertThat(vm.state.value.confirmRemove).isNull()
        assertThat(source.calls).containsExactly("connections(other)")
    }

    @Test
    fun `a failed first read says so, and switching tabs reads each tab once`() = runTest {
        val source = FakeConnections().apply {
            people = AppResult.Failure(AppError.Unknown(code = "INTERNAL_ERROR", statusCode = 500))
        }
        val vm = viewModel(source)

        assertThat(vm.state.value.connections.error).isNotNull()

        vm.select(ConnectionsTab.SENT)
        vm.select(ConnectionsTab.CONNECTIONS)
        vm.select(ConnectionsTab.SENT)

        assertThat(source.calls.count { it == "requests(${ConnectionRequestDirection.SENT})" }).isEqualTo(1)
    }

    @Test
    fun `the profile's connection control follows the status`() {
        assertThat(connectionControlFor(ConnectionStatus.NONE, offered = false)).isNull()
        assertThat(connectionControlFor(ConnectionStatus.NONE, offered = true)).isEqualTo(ConnectionControl.CONNECT)
        assertThat(connectionControlFor(ConnectionStatus.PENDING_SENT)).isEqualTo(ConnectionControl.REQUEST_SENT)
        assertThat(connectionControlFor(ConnectionStatus.PENDING_RECEIVED)).isEqualTo(ConnectionControl.RESPOND)
        assertThat(connectionControlFor(ConnectionStatus.CONNECTED)).isEqualTo(ConnectionControl.CONNECTED)
        // As on the web: nothing STARTS a connection today.
        assertThat(CONNECT_OFFERED).isFalse()
    }

    @Test
    fun `the title is Connections on the viewer's own and the handle on anyone else's`() {
        assertThat(connectionsTitle("ana", isOwn = false)).isEqualTo("@ana")
        assertThat(connectionsTitle("ana", isOwn = true)).isEqualTo("Connections")
        assertThat(connectionsTitle("", isOwn = false)).isEqualTo("Connections")
    }
}
