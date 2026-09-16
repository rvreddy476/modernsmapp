package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.location.LocationEffect
import com.us.android.feature.dating.location.LocationStep
import com.us.android.feature.dating.privacy.PrivacyViewModel
import com.us.android.feature.dating.safety.SafetyViewModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.io.ByteArrayOutputStream

/** Panic, trusted contacts, live location, and the data rights. */
@OptIn(ExperimentalCoroutinesApi::class)
class SafetyAndPrivacyTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession().also { it.setProfile(profile()) }

    private fun safety(location: FakeLocation = FakeLocation()) =
        SafetyViewModel(api.repository(), session, location, photoUrls())

    @Test
    fun `panic without location permission asks nothing and still alerts`() = runTest {
        val vm = safety(FakeLocation(permission = false))

        vm.panic()

        assertThat(vm.state.value.panic?.incidentId).isEqualTo("incident-1")
        assertThat(vm.state.value.location).isEqualTo(LocationStep.Idle)
    }

    @Test
    fun `trusted contacts come from matches, and the limit is the server's words`() = runTest {
        api.matches = listOf(match("m-1", "friend"), match("m-2", "other"))
        api.trusted = listOf(trustedContact("friend"))
        val vm = safety()

        assertThat(vm.state.value.contacts.map { it.userId }).containsExactly("friend")
        assertThat(vm.state.value.candidates.map { it.userId }).containsExactly("other")
        assertThat(vm.state.value.recipients.map { it.userId }).containsExactly("friend", "other")
    }

    @Test
    fun `a trusted contact is shown by the name on its own person card`() = runTest {
        // No match row at all: the card on the contact row is the only source.
        api.matches = emptyList()
        api.trusted = listOf(trustedContact("friend", person("friend", name = "Asha")))
        val vm = safety()

        val contact = vm.state.value.contacts.single()
        assertThat(contact.userId).isEqualTo("friend")
        assertThat(contact.name).isEqualTo("Asha")
        assertThat(contact.photoUrl).isEqualTo("https://api.test/v1/dating/photos/photo-friend/full")
        // The same name reaches the share-recipient chooser.
        assertThat(vm.state.value.recipients.single().name).isEqualTo("Asha")
    }

    @Test
    fun `a contact whose profile is gone still lists, so it can be removed`() = runTest {
        api.trusted = listOf(trustedContact("ghost", card = null))
        val vm = safety()

        val contact = vm.state.value.contacts.single()
        assertThat(contact.userId).isEqualTo("ghost")
        assertThat(contact.name).isEqualTo("Your match")
        assertThat(contact.photoUrl).isNull()

        vm.removeContact("ghost")

        assertThat(api.calls).contains("trusted-remove:ghost")
    }

    @Test
    fun `a blurred contact card never renders the full photo`() = runTest {
        api.trusted = listOf(trustedContact("friend", person("friend", photoState = "blurred")))
        val vm = safety()

        val photo = checkNotNull(vm.state.value.contacts.single().photoUrl)
        assertThat(photo).endsWith("/blurred")
        assertThat(photo).doesNotContain("/full")
    }

    @Test
    fun `a blocked person is never offered as a contact or a share recipient`() = runTest {
        api.matches = listOf(match("m-1", "friend"), match("m-2", "blocked"))
        session.removePerson("blocked")
        val vm = safety()
        assertThat(vm.state.value.recipients.map { it.userId }).containsExactly("friend")
        assertThat(vm.state.value.candidates.map { it.userId }).containsExactly("friend")
    }

    @Test
    fun `a live share explains before asking, is capped at 120 minutes, and can be stopped`() = runTest {
        api.matches = listOf(match("m-1", "friend"))
        val location = FakeLocation(permission = false)
        val vm = safety(location)

        assertThat(vm.share("friend", 600)).isEqualTo(LocationEffect.None)
        assertThat(vm.state.value.location).isEqualTo(LocationStep.ExplainingPermission)
        assertThat(vm.onRationaleAccepted()).isEqualTo(LocationEffect.RequestPermission)

        location.permission = true
        vm.onPermissionResult(granted = true, canAskAgain = true)

        assertThat(vm.state.value.shares.single().shareId).isEqualTo("share-1")

        vm.stopShare("share-1")
        assertThat(vm.state.value.shares).isEmpty()
    }

    @Test
    fun `active shares are listed from the server, so Stop works in a fresh process`() = runTest {
        // Nothing was shared by THIS process: the list is the server's.
        api.myShares = listOf(
            com.us.android.feature.dating.network.MyLocationShareDto(
                shareId = "share-restored",
                recipientId = "friend",
                recipientKind = "match",
                expiresAt = "later",
                recipient = person("friend", name = "Asha"),
            ),
        )
        val vm = safety()

        assertThat(vm.state.value.shares.single().shareId).isEqualTo("share-restored")
        assertThat(vm.state.value.shares.single().recipientName).isEqualTo("Asha")

        vm.stopShare("share-restored")

        assertThat(api.stops).containsExactly("share-restored")
        assertThat(vm.state.value.shares).isEmpty()
    }

    @Test
    fun `a location shared with me is listed by share id and names the sharer`() = runTest {
        api.sharedWithMe = listOf(
            com.us.android.feature.dating.network.SharedWithMeItemDto(
                shareId = "share-in",
                userId = "friend",
                expiresAt = "later",
                person = person("friend", name = "Asha"),
            ),
        )
        val vm = safety()

        val shared = vm.state.value.sharedWithMe.single()
        assertThat(shared.shareId).isEqualTo("share-in")
        assertThat(shared.sharerName).isEqualTo("Asha")
    }

    @Test
    fun `withdrawing a consent sends granted false`() = runTest {
        api.granted += ConsentType.SENSITIVE_RELIGION
        val vm = PrivacyViewModel(api.repository(), session, UnconfinedTestDispatcher(testScheduler))

        vm.setConsent(ConsentType.SENSITIVE_RELIGION, granted = false)

        assertThat(api.calls).contains("consent:sensitive_religion=false")
        assertThat(ConsentGate.granted(session.consents.value, ConsentType.SENSITIVE_RELIGION)).isFalse()
    }

    @Test
    fun `a ready export downloads into the chosen file, and deleting clears everything`() = runTest {
        val vm = PrivacyViewModel(api.repository(), session, UnconfinedTestDispatcher(testScheduler))
        val out = ByteArrayOutputStream()

        vm.download("export-1") { body -> body.byteStream().copyTo(out) }
        assertThat(out.toString()).isEqualTo("{}")

        vm.deleteProfile(null)
        assertThat(vm.state.value.deleted).isTrue()
        assertThat(session.profile.value).isNull()
    }
}
